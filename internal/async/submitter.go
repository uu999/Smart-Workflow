package async

import (
	"context"

	"go.uber.org/zap"
)

// Submitter 用 asynq 入队实现 api.RunSubmitter（Submit + Shutdown）。
// 它是 M9-a 对进程内 RunDispatcher 的替代：Submit=入队，Shutdown=关 client。
// run 的真正执行由 swf-worker 消费队列时调 engine.Run 完成。
//
// 结构化满足 api.RunSubmitter 接口（不直接 import api，避免环）。
type Submitter struct {
	enq    *Enqueuer
	queue  string
	logger *zap.Logger
}

// NewSubmitter 构造入队提交器；queue 为空时用 default 队列。
func NewSubmitter(enq *Enqueuer, queue string, logger *zap.Logger) *Submitter {
	if queue == "" {
		queue = QueueDefault
	}
	if logger == nil {
		logger = zap.NewNop()
	}
	return &Submitter{enq: enq, queue: queue, logger: logger}
}

// Submit 把 run 入队。入队失败（Redis 不可达等）返回 false，
// 调用方（createRun）据此把 run 落 failed 并回 503——与 dispatcher 语义一致。
func (s *Submitter) Submit(runID string) bool {
	accepted, err := s.enq.Enqueue(runID, s.queue)
	if err != nil {
		s.logger.Error("enqueue run failed", zap.String("run_id", runID), zap.Error(err))
		return false
	}
	s.logger.Info("run enqueued", zap.String("run_id", runID), zap.String("queue", s.queue))
	return accepted
}

// Shutdown 关闭入队客户端（worker 的停机由 worker 进程自己管）。
func (s *Submitter) Shutdown(_ context.Context) error {
	return s.enq.Close()
}
