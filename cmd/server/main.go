package main

import (
	"context"
	"errors"
	"flag"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"go.uber.org/zap"

	"github.com/smart-workflow/smart-workflow/internal/api"
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
	flag.Parse()

	cfg, err := config.Load(*configPath)
	if err != nil {
		panic(err)
	}

	logger := logx.Must(cfg.Log.Level)
	defer func() { _ = logger.Sync() }()

	// M6：接上存储与引擎，打通 config→server→engine→code→sidecar 全链路（清 TD-7）。
	store, err := mysql.Open(cfg.MySQL.DSN)
	if err != nil {
		logger.Fatal("connect mysql failed", zap.String("dsn_host", cfg.MySQL.DSN), zap.Error(err))
	}
	defer func() { _ = store.Close() }()

	eng := engine.New(store, cfg.Sidecar.BaseURL).WithLogger(logger)

	// M9-a：Redis 配了就启用定义缓存（已发布 DSL 不可变，命中省 MySQL 读）。
	var redisStore *cache.RedisStore
	if cfg.Redis.Addr != "" {
		redisStore = cache.NewRedisStore(cfg.Redis)
		defer func() { _ = redisStore.Close() }()
		eng = eng.WithCache(redisStore)
	}

	// 后台运行提交器（RunSubmitter）+ 运行事件源（M9-b SSE），按是否配 Redis 分两条路径：
	//   - Redis 配了 → asynq：run 由 swf-worker 进程执行并发事件（RedisEmitter），
	//       server 端 SSE 从 Redis Stream 订阅（RedisSource）。跨进程。
	//   - 否则 → 进程内：RunDispatcher 兜底执行，engine 与 SSE 同进程共享一个 MemHub
	//       （既作 engine 的 Emitter，又作 SSE 的 Source）。
	var runner api.RunSubmitter
	var eventSource eventbus.Source
	var sseSourceClose func()
	if cfg.Redis.Addr != "" {
		enq := async.NewEnqueuer(cfg.Redis)
		runner = async.NewSubmitter(enq, async.QueueDefault, logger)
		// SSE 事件源：与 cache 共享 Redis 连接配置（另建 client，SSE 长读不占缓存连接池）。
		sseCli := cache.NewRedisClient(cfg.Redis)
		sseSourceClose = func() { _ = sseCli.Close() }
		eventSource = eventbus.NewRedisSource(sseCli)
		logger.Info("run backend: asynq (enqueue to worker)", zap.String("redis", cfg.Redis.Addr))
		logger.Info("event stream: redis stream (cross-process)")
	} else {
		hub := eventbus.NewMemHub()
		eng = eng.WithEmitter(hub) // 进程内执行时事件直接进 Hub
		eventSource = hub          // SSE 从同一个 Hub 订阅
		runner = api.NewRunDispatcher(eng, logger, 0)
		logger.Info("run backend: in-process dispatcher (no redis configured)")
		logger.Info("event stream: in-process memhub")
	}
	if sseSourceClose != nil {
		defer sseSourceClose()
	}

	router := api.NewRouter(api.Deps{
		Cfg:         cfg,
		Logger:      logger,
		Store:       store,
		Engine:      eng,
		Runner:      runner,
		EventSource: eventSource,
	})

	srv := &http.Server{
		Addr:    cfg.Server.Addr,
		Handler: router,
	}

	// 后台启动
	go func() {
		logger.Info("server starting", zap.String("addr", cfg.Server.Addr), zap.String("env", cfg.Env))
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Fatal("server error", zap.Error(err))
		}
	}()

	// 优雅退出：收到 SIGINT/SIGTERM 后，给在途请求 10s 收尾
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	logger.Info("server shutting down")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		logger.Error("graceful shutdown failed", zap.Error(err))
	}
	// HTTP 已停收新请求，再收尾后台 run 提交器（dispatcher drain / asynq client 关闭）。
	if err := runner.Shutdown(ctx); err != nil {
		logger.Warn("run submitter shutdown issue", zap.Error(err))
	}
	logger.Info("server stopped")
}
