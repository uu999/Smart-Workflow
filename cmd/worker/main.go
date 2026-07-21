// Command swf-worker 是 asynq 消费者：从 Redis 队列取工作流运行任务，
// 调用 engine.Run 执行。与 swf-server 复用同一 engine.Run 纯函数入口（M9-a）。
//
// 服务重启后在途任务仍在 Redis，worker 重启会继续消费 —— 天然"捡回在途任务"。
package main

import (
	"context"
	"flag"
	"time"

	"go.uber.org/zap"

	"github.com/smart-workflow/smart-workflow/internal/async"
	"github.com/smart-workflow/smart-workflow/internal/cache"
	"github.com/smart-workflow/smart-workflow/internal/config"
	"github.com/smart-workflow/smart-workflow/internal/engine"
	"github.com/smart-workflow/smart-workflow/internal/eventbus"
	"github.com/smart-workflow/smart-workflow/internal/logx"
	"github.com/smart-workflow/smart-workflow/internal/storage/mysql"
)

func main() {
	configPath := flag.String("config", "", "path to config file (optional)")
	concurrency := flag.Int("concurrency", 0, "worker concurrency (0 = asynq default)")
	flag.Parse()

	cfg, err := config.Load(*configPath)
	if err != nil {
		panic(err)
	}

	logger := logx.Must(cfg.Log.Level)
	defer func() { _ = logger.Sync() }()

	if cfg.Redis.Addr == "" {
		logger.Fatal("worker requires redis; set SWF_REDIS_ADDR or config redis.addr")
	}

	store, err := mysql.Open(cfg.MySQL.DSN)
	if err != nil {
		logger.Fatal("connect mysql failed", zap.Error(err))
	}
	defer func() { _ = store.Close() }()

	// 与 server 一致：engine 复用 + 定义缓存（命中省 MySQL 读）。
	redisStore := cache.NewRedisStore(cfg.Redis)
	defer func() { _ = redisStore.Close() }()

	// M9-b：worker 执行 run 时把节点/终态事件写入 Redis Stream，供 server 端 SSE 订阅。
	// 单独一条连接（Stream 写与缓存读互不干扰），退出时排空缓冲再关。
	sseCli := cache.NewRedisClient(cfg.Redis)
	emitter := eventbus.NewRedisEmitter(sseCli)
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = emitter.Close(ctx)
		_ = sseCli.Close()
	}()

	eng := engine.New(store, cfg.Sidecar.BaseURL).
		WithLogger(logger).
		WithCache(redisStore).
		WithEmitter(emitter)

	srv := async.NewServer(cfg.Redis, async.ServerConfig{Concurrency: *concurrency})
	mux := async.NewMux(eng)

	logger.Info("swf-worker starting",
		zap.String("redis", cfg.Redis.Addr),
		zap.Int("concurrency", *concurrency),
	)
	// Run 阻塞直到收到 SIGINT/SIGTERM，asynq 内部处理优雅退出（等在途任务收尾）。
	if err := srv.Run(mux); err != nil {
		logger.Fatal("worker stopped with error", zap.Error(err))
	}
	logger.Info("swf-worker stopped")
}
