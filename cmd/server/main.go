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
	"github.com/smart-workflow/smart-workflow/internal/config"
	"github.com/smart-workflow/smart-workflow/internal/engine"
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

	eng := engine.New(store, cfg.Sidecar.BaseURL)

	router := api.NewRouter(api.Deps{
		Cfg:    cfg,
		Logger: logger,
		Store:  store,
		Engine: eng,
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
	logger.Info("server stopped")
}
