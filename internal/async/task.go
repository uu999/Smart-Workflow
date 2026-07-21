// Package async 实现 M9-a 的异步执行：把工作流运行入队 Redis（asynq），
// 由 swf-worker 消费时调用 engine.Run —— 复用 M4 的纯函数入口，
// worker 只是 engine.Run 的又一个调用方（同步 handler 是另一个）。
//
// 关键设计：
//   - 任务 payload 只带 runID：状态/输入/输出全在 DB，worker 无需额外上下文；
//   - TaskID(runID) 去重：同一 run 不会被重复入队/执行，天然充当"运行锁"；
//   - 优先级队列：debug > default > batch，调试即时反馈优先、批量靠后；
//   - MaxRetry + engine 内部墓碑：失败重试，重试耗尽后 run 已被 engine 落 failed。
package async

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/hibiken/asynq"
)

// TypeRunWorkflow 是工作流运行任务的类型名。
const TypeRunWorkflow = "swf:run"

// 优先级队列名（数值权重在 worker 的 Server.Queues 配置）。
const (
	QueueDebug   = "debug"   // node-debug / 交互式，最高优先
	QueueDefault = "default" // 普通 run
	QueueBatch   = "batch"   // 批量任务，最低优先
)

// RunPayload 是运行任务的载荷：只带 runID，其余状态在 DB。
type RunPayload struct {
	RunID string `json:"run_id"`
}

// NewRunTask 构造一个运行任务，绑定到指定队列，并用 runID 做去重 + 重试配置。
func NewRunTask(runID, queue string, maxRetry int) (*asynq.Task, error) {
	if runID == "" {
		return nil, fmt.Errorf("async: empty run id")
	}
	payload, err := json.Marshal(RunPayload{RunID: runID})
	if err != nil {
		return nil, fmt.Errorf("async: marshal payload: %w", err)
	}
	if queue == "" {
		queue = QueueDefault
	}
	return asynq.NewTask(
		TypeRunWorkflow, payload,
		asynq.Queue(queue),
		asynq.TaskID(runID), // 去重锁：同 runID 重复入队会被 asynq 拒绝
		asynq.MaxRetry(maxRetry),
	), nil
}

// decodeRunPayload 从任务里解出 RunPayload（worker 侧用）。
func decodeRunPayload(t *asynq.Task) (RunPayload, error) {
	var p RunPayload
	if err := json.Unmarshal(t.Payload(), &p); err != nil {
		return p, fmt.Errorf("async: unmarshal payload: %w", err)
	}
	if p.RunID == "" {
		return p, fmt.Errorf("async: payload missing run_id")
	}
	return p, nil
}

// RunExecutor 是 worker 执行一个 run 所需的最小能力（engine.Engine 满足）。
// 抽成接口便于单测注入假执行器，不必起真 engine。
type RunExecutor interface {
	Run(ctx context.Context, runID string) error
}

// RunProcessor 是 asynq.Handler：解 payload → 调 engine.Run。
// 返回非 nil error 触发 asynq 重试；engine.Run 内部已对失败落 failed 墓碑，
// 故重试耗尽也不会留下僵尸 pending/running。
type RunProcessor struct {
	exec RunExecutor
}

// NewRunProcessor 构造处理器。
func NewRunProcessor(exec RunExecutor) *RunProcessor {
	return &RunProcessor{exec: exec}
}

// ProcessTask 实现 asynq.Handler。
func (p *RunProcessor) ProcessTask(ctx context.Context, t *asynq.Task) error {
	payload, err := decodeRunPayload(t)
	if err != nil {
		// 载荷坏了重试也没用：返回 SkipRetry 让 asynq 归档到 dead 队列。
		return fmt.Errorf("%w: %v", asynq.SkipRetry, err)
	}
	return p.exec.Run(ctx, payload.RunID)
}
