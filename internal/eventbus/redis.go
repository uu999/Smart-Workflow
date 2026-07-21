package eventbus

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/smart-workflow/smart-workflow/internal/cache"
	"github.com/smart-workflow/smart-workflow/internal/runevent"
)

// 编译期断言：Redis 两端分别满足 Emitter / Source。
var (
	_ runevent.Emitter = (*RedisEmitter)(nil)
	_ Source           = (*RedisSource)(nil)
)

const (
	streamMaxLen = 1000            // 每个 run 事件流的近似上限，防无界增长
	emitBuffer   = 256             // RedisEmitter 异步发送缓冲
	emitTimeout  = 3 * time.Second // 单次 XADD/Expire 超时
	streamTTL    = 6 * time.Hour   // run 结束后事件流保留时长（供短暂重连/回看）
	xreadBlock   = 5 * time.Second // XREAD 阻塞时长（到点回环，顺带检查 ctx）
	xreadCount   = 128             // 单次 XREAD 最多取回条数
	xreadBackoff = 500 * time.Millisecond
)

// StreamKey 返回某 run 的事件流键。复用 cache.RunKey 的 swf:run:{id} 前缀再加
// :events 后缀，保证与运行态镜像键同源、不因改前缀而漂移。
func StreamKey(runID string) string {
	return cache.RunKey(runID) + ":events"
}

// RedisEmitter 把运行事件写入 Redis Stream（跨进程：worker 发、server 的 SSE 读）。
//
// 关键点：Emitter.Emit 在 scheduler 单线程 loop 内被同步调用，绝不能让 Redis 时延
// 拖住节点派发。因此这里做成「异步缓冲发布者」——Emit 只入本地 channel（非阻塞，
// 与 MemHub 语义一致：满则丢中间事件，但 run_end 尽力送达），由后台 drain 顺序 XADD。
// 单一 drain goroutine 保证 FIFO，run_end 不会抢先于节点事件落库到流里。
type RedisEmitter struct {
	cli  *redis.Client
	ch   chan runevent.RunEvent
	stop chan struct{}
	done chan struct{}
}

// NewRedisEmitter 构造并启动后台发送协程。调用方负责在退出时 Close。
func NewRedisEmitter(cli *redis.Client) *RedisEmitter {
	e := &RedisEmitter{
		cli:  cli,
		ch:   make(chan runevent.RunEvent, emitBuffer),
		stop: make(chan struct{}),
		done: make(chan struct{}),
	}
	go e.drain()
	return e
}

// Emit 非阻塞入队。缓冲满时丢弃中间事件；run_end 例外——它是流的关闭信号，
// 若丢失订阅端将永不收敛，故满时退化为「与 stop 竞争」的阻塞写以尽力送达。
func (e *RedisEmitter) Emit(evt runevent.RunEvent) {
	select {
	case e.ch <- evt:
	case <-e.stop:
	default:
		if evt.Phase == runevent.PhaseRunEnd {
			select {
			case e.ch <- evt:
			case <-e.stop:
			}
		}
	}
}

// drain 顺序消费缓冲并 XADD。收到 stop 后尽力排空剩余事件再退出（不丢 run_end）。
func (e *RedisEmitter) drain() {
	defer close(e.done)
	for {
		select {
		case <-e.stop:
			for {
				select {
				case evt := <-e.ch:
					e.write(evt)
				default:
					return
				}
			}
		case evt := <-e.ch:
			e.write(evt)
		}
	}
}

// write 执行一次 XADD；run_end 落流后给 key 设 TTL，避免事件流永久驻留。
// SSE 是尽力而为的实时视图，权威状态在 MySQL，故发送失败仅静默丢弃。
func (e *RedisEmitter) write(evt runevent.RunEvent) {
	b, err := json.Marshal(evt)
	if err != nil {
		return // RunEvent 字段均可序列化，不应发生
	}
	ctx, cancel := context.WithTimeout(context.Background(), emitTimeout)
	defer cancel()
	key := StreamKey(evt.RunID)
	_ = e.cli.XAdd(ctx, &redis.XAddArgs{
		Stream: key,
		MaxLen: streamMaxLen,
		Approx: true,
		Values: map[string]any{"data": b},
	}).Err()
	if evt.Phase == runevent.PhaseRunEnd {
		_ = e.cli.Expire(ctx, key, streamTTL).Err()
	}
}

// Close 停止后台协程并等待缓冲排空（受 ctx 截止约束）。
func (e *RedisEmitter) Close(ctx context.Context) error {
	close(e.stop)
	select {
	case <-e.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// RedisSource 订阅某 run 的 Redis Stream 并转成事件 channel（SSE handler 消费）。
type RedisSource struct {
	cli *redis.Client
}

// NewRedisSource 用已有 client 构造订阅端（与 emitter 共享连接配置）。
func NewRedisSource(cli *redis.Client) *RedisSource {
	return &RedisSource{cli: cli}
}

// Subscribe 从流头（"0"）开始阻塞读：run 可能已在 worker 端开跑，从头读可补齐
// 已产生的历史事件（含最早的 node_start），不漏拍。遇 run_end 或 ctx 取消关闭 channel。
func (s *RedisSource) Subscribe(ctx context.Context, runID string) (<-chan runevent.RunEvent, error) {
	out := make(chan runevent.RunEvent, subBuffer)
	key := StreamKey(runID)
	go func() {
		defer close(out)
		lastID := "0"
		for {
			if ctx.Err() != nil {
				return
			}
			res, err := s.cli.XRead(ctx, &redis.XReadArgs{
				Streams: []string{key, lastID},
				Block:   xreadBlock,
				Count:   xreadCount,
			}).Result()
			if err != nil {
				if errors.Is(err, redis.Nil) {
					continue // BLOCK 到点无新消息：回环再等（顺带查 ctx）
				}
				if ctx.Err() != nil {
					return // ctx 取消导致的读中断
				}
				// 其他抖动：退避后重试，不因一次读失败终止整条流
				select {
				case <-ctx.Done():
					return
				case <-time.After(xreadBackoff):
				}
				continue
			}
			for _, stream := range res {
				for _, msg := range stream.Messages {
					lastID = msg.ID
					evt, ok := decodeMessage(msg)
					if !ok {
						continue // 脏消息跳过，但推进 lastID 避免卡住
					}
					select {
					case out <- evt:
					case <-ctx.Done():
						return
					}
					if evt.Phase == runevent.PhaseRunEnd {
						return // 流结束信号
					}
				}
			}
		}
	}()
	return out, nil
}

// decodeMessage 从 stream 消息还原 RunEvent（data 字段存整条事件 JSON）。
func decodeMessage(msg redis.XMessage) (runevent.RunEvent, bool) {
	raw, ok := msg.Values["data"].(string)
	if !ok {
		return runevent.RunEvent{}, false
	}
	var evt runevent.RunEvent
	if err := json.Unmarshal([]byte(raw), &evt); err != nil {
		return runevent.RunEvent{}, false
	}
	return evt, true
}
