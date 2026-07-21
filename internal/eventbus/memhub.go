package eventbus

import (
	"context"
	"sync"

	"github.com/smart-workflow/smart-workflow/internal/runevent"
)

// 编译期断言：MemHub 同时是发布端（Emitter）与订阅端（Source）。
var (
	_ runevent.Emitter = (*MemHub)(nil)
	_ Source           = (*MemHub)(nil)
)

// subBuffer 是每个订阅者的缓冲大小。SSE 是实时视图，慢消费者不应阻塞发布端
// （发布端可能是单线程调度 loop）；缓冲满时丢弃中间事件，但 run_end 通过关闭 channel 保证送达。
const subBuffer = 64

// MemHub 是进程内事件总线：同时实现 runevent.Emitter（发布）与 Source（订阅）。
// 用于无 Redis 的进程内 dispatcher 模式（engine 与 SSE 同进程）。
type MemHub struct {
	mu   sync.Mutex
	subs map[string][]chan runevent.RunEvent // runID -> 订阅者 channels
}

// NewMemHub 构造一个空的内存总线。
func NewMemHub() *MemHub {
	return &MemHub{subs: make(map[string][]chan runevent.RunEvent)}
}

// Emit 把事件分发给该 runID 的所有订阅者（非阻塞：缓冲满则丢该事件）。
// run_end 事件分发后关闭并清理所有订阅者 channel（流结束信号）。
func (h *MemHub) Emit(evt runevent.RunEvent) {
	h.mu.Lock()
	defer h.mu.Unlock()
	chans := h.subs[evt.RunID]
	for _, ch := range chans {
		select {
		case ch <- evt:
		default: // 慢消费者：丢弃中间事件，不阻塞发布端
		}
	}
	if evt.Phase == runevent.PhaseRunEnd {
		for _, ch := range chans {
			close(ch)
		}
		delete(h.subs, evt.RunID)
	}
}

// Subscribe 注册一个订阅者。ctx 取消时自动退订并关闭 channel（防泄漏）。
func (h *MemHub) Subscribe(ctx context.Context, runID string) (<-chan runevent.RunEvent, error) {
	ch := make(chan runevent.RunEvent, subBuffer)
	h.mu.Lock()
	h.subs[runID] = append(h.subs[runID], ch)
	h.mu.Unlock()

	// ctx 取消 → 退订（若尚未被 run_end 关闭）。
	go func() {
		<-ctx.Done()
		h.unsubscribe(runID, ch)
	}()
	return ch, nil
}

// unsubscribe 移除并关闭一个订阅者 channel（幂等：已被 run_end 清理则跳过）。
func (h *MemHub) unsubscribe(runID string, target chan runevent.RunEvent) {
	h.mu.Lock()
	defer h.mu.Unlock()
	chans, ok := h.subs[runID]
	if !ok {
		return // 已被 run_end 清理
	}
	kept := chans[:0]
	for _, ch := range chans {
		if ch == target {
			close(ch)
			continue
		}
		kept = append(kept, ch)
	}
	if len(kept) == 0 {
		delete(h.subs, runID)
	} else {
		h.subs[runID] = kept
	}
}
