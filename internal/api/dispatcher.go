package api

import (
	"context"
	"fmt"
	"sync"
	"time"

	"go.uber.org/zap"

	"github.com/smart-workflow/smart-workflow/internal/engine"
)

// RunDispatcher 是 M6 的后台运行调度器，修复裸 goroutine 的三重缺陷（硬伤1）：
//
//  1. panic 兜底：每条运行 goroutine 内 recover，崩溃只让该 run 落 failed，
//     不再打崩整个 server 进程（gin.Recovery 护不到我们自起的 goroutine）。
//  2. 并发上限：semaphore 限制同时在跑的 run 数，避免批量提交 OOM / 打爆 sidecar。
//  3. 优雅退出：baseCtx 可取消 + WaitGroup 可 drain，Shutdown 时等在途 run 收尾，
//     不再用 context.Background() 逃逸 srv.Shutdown。
//
// 这是「M9 换 asynq 之前」的进程内兜底：HTTP 契约（提交即返回 runID）不变，
// M9 只需把 Submit 换成入队、Shutdown 换成停消费者。
type RunDispatcher struct {
	engine  *engine.Engine
	logger  *zap.Logger
	sem     chan struct{}
	wg      sync.WaitGroup
	baseCtx context.Context
	cancel  context.CancelFunc

	mu      sync.Mutex
	closed  bool
}

// NewRunDispatcher 构造调度器。maxConcurrent <= 0 时取默认 16。
func NewRunDispatcher(eng *engine.Engine, logger *zap.Logger, maxConcurrent int) *RunDispatcher {
	if maxConcurrent <= 0 {
		maxConcurrent = 16
	}
	if logger == nil {
		logger = zap.NewNop()
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &RunDispatcher{
		engine:  eng,
		logger:  logger,
		sem:     make(chan struct{}, maxConcurrent),
		baseCtx: ctx,
		cancel:  cancel,
	}
}

// Submit 尝试后台执行一个已落 pending 的 run：
//   - 已关停 → 返回 false（调用方应把 run 落 failed 并回 503）
//   - semaphore 满 → 返回 false（背压：拒绝而非无限堆积）
//   - 成功占坑 → 起 goroutine 执行，返回 true
//
// 用独立 baseCtx（不随单个 HTTP 请求取消），但 Shutdown 会取消它。
func (d *RunDispatcher) Submit(runID string) bool {
	d.mu.Lock()
	if d.closed {
		d.mu.Unlock()
		d.logger.Info("run rejected: dispatcher shutting down", zap.String("run_id", runID))
		return false
	}
	// 占坑要在持锁期间尝试，避免与 Shutdown 竞争后仍加 wg。
	select {
	case d.sem <- struct{}{}:
		d.wg.Add(1)
		inFlight := len(d.sem)
		d.mu.Unlock()
		d.logger.Info("run accepted",
			zap.String("run_id", runID),
			zap.Int("in_flight", inFlight),
			zap.Int("capacity", cap(d.sem)),
		)
	default:
		d.mu.Unlock()
		d.logger.Warn("run rejected: dispatcher at capacity",
			zap.String("run_id", runID), zap.Int("capacity", cap(d.sem)))
		return false // 满载，背压拒绝
	}

	go func() {
		startedAt := time.Now()
		defer d.wg.Done()
		defer func() { <-d.sem }()
		defer func() {
			if r := recover(); r != nil {
				d.logger.Error("run panicked", zap.String("run_id", runID), zap.Any("panic", r))
				// panic 后用独立 ctx 落墓碑，不复用可能已污染的 baseCtx 状态。
				if err := d.engine.FailRunWithError(context.Background(), runID,
					fmt.Sprintf("run panicked: %v", r)); err != nil {
					d.logger.Error("failed to mark panicked run as failed",
						zap.String("run_id", runID), zap.Error(err))
				}
			}
		}()

		d.logger.Info("run goroutine started", zap.String("run_id", runID))
		if err := d.engine.Run(d.baseCtx, runID); err != nil {
			d.logger.Warn("run goroutine finished with error",
				zap.String("run_id", runID),
				zap.Duration("elapsed", time.Since(startedAt)),
				zap.Error(err))
			return
		}
		d.logger.Info("run goroutine finished ok",
			zap.String("run_id", runID),
			zap.Duration("elapsed", time.Since(startedAt)))
	}()
	return true
}

// Shutdown 优雅停机：停止接收新 run、取消在途 run 的 ctx，
// 然后等所有在途 goroutine 收尾或 ctx 到期。
func (d *RunDispatcher) Shutdown(ctx context.Context) error {
	d.mu.Lock()
	if d.closed {
		d.mu.Unlock()
		return nil
	}
	d.closed = true
	inFlight := len(d.sem)
	d.mu.Unlock()

	d.logger.Info("dispatcher shutting down: draining in-flight runs",
		zap.Int("in_flight", inFlight))
	d.cancel() // 通知在途 run 尽快收尾（engine.Run 内部感知 ctx 取消）

	done := make(chan struct{})
	go func() {
		d.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		d.logger.Info("dispatcher drained: all in-flight runs finished")
		return nil
	case <-ctx.Done():
		d.logger.Warn("dispatcher drain timed out", zap.Error(ctx.Err()))
		return ctx.Err()
	}
}
