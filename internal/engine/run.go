package engine

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"go.uber.org/zap"

	"github.com/smart-workflow/smart-workflow/internal/dsl"
	"github.com/smart-workflow/smart-workflow/internal/engine/builder"
	"github.com/smart-workflow/smart-workflow/internal/engine/nodes"
	"github.com/smart-workflow/smart-workflow/internal/engine/scheduler"
	"github.com/smart-workflow/smart-workflow/internal/engine/varpool"
	"github.com/smart-workflow/smart-workflow/internal/storage/mysql"
	"github.com/smart-workflow/smart-workflow/internal/storage/mysql/gen"
)

const (
	DraftVersion = -1

	RunStatusPending   = "pending"
	RunStatusRunning   = "running"
	RunStatusSucceeded = "succeeded"
	RunStatusFailed    = "failed"
	RunStatusCanceled  = "canceled"
)

// Engine 是工作流执行入口。Run(ctx, runID) 只依赖数据库里的 run 记录，
// 因而后续同步 HTTP handler 与异步 worker 都可以复用同一入口。
//
// 改造①②：Engine 持有节点执行器注册表（含配置化的 code sidecar 地址），
// 不再依赖 nodes 包级全局注册；每次 scheduler.Run 用它注入执行器。
type Engine struct {
	store       *mysql.Store
	registry    *nodes.Registry
	logger      *zap.Logger
	Concurrency int
}

// New 用给定 sidecar 地址构造引擎。sidecarURL 通常来自 config.Sidecar.BaseURL，
// 打通了「配置 → code 节点实际调用地址」这条链路。
func New(store *mysql.Store, sidecarURL string) *Engine {
	return &Engine{
		store:       store,
		registry:    nodes.NewDefaultRegistry(nodes.Config{SidecarURL: sidecarURL}),
		logger:      zap.NewNop(),
		Concurrency: 8,
	}
}

// WithLogger 注入结构化日志器（返回自身便于链式调用）。传 nil 时保持 Nop。
func (e *Engine) WithLogger(l *zap.Logger) *Engine {
	if l != nil {
		e.logger = l
	}
	return e
}

// log 返回非空 logger（防御结构体字面量构造时 logger 为 nil）。
func (e *Engine) log() *zap.Logger {
	if e.logger == nil {
		return zap.NewNop()
	}
	return e.logger
}

// Run 执行一个已创建的 workflow_run：
// 读 run -> 取发布版本/草稿 DSL -> 构建计划 -> 调度执行 -> 写 workflow_run/node_run。
func (e *Engine) Run(ctx context.Context, runID string) error {
	if e == nil || e.store == nil {
		return fmt.Errorf("engine: nil store")
	}
	log := e.log().With(zap.String("run_id", runID))

	run, err := e.store.Q.GetWorkflowRun(ctx, runID)
	if err != nil {
		log.Error("run stage: load run record failed", zap.Error(err))
		return fmt.Errorf("engine: get run %s: %w", runID, err)
	}
	log.Info("run stage: start",
		zap.String("workflow_id", run.WorkflowID),
		zap.Int32("version", run.Version),
		zap.String("trigger", run.TriggerType),
	)

	startedAt := time.Now()
	if err := e.updateRun(ctx, runID, RunStatusRunning, nil, "", startedAt, time.Time{}); err != nil {
		log.Error("run stage: mark running failed", zap.Error(err))
		return fmt.Errorf("engine: mark run running: %w", err)
	}
	log.Info("run stage: marked running")

	input, err := decodeInput(run.Input)
	if err != nil {
		log.Warn("run stage: decode input failed", zap.Error(err))
		return e.failRun(ctx, runID, startedAt, fmt.Errorf("engine: decode run input: %w", err))
	}

	workflowDSL, err := e.loadDSL(ctx, run.WorkflowID, run.Version)
	if err != nil {
		log.Warn("run stage: load dsl failed", zap.Error(err))
		return e.failRun(ctx, runID, startedAt, err)
	}
	log.Info("run stage: dsl loaded",
		zap.Bool("is_draft", run.Version == DraftVersion),
		zap.Int("node_count", len(workflowDSL.Nodes)),
		zap.Int("edge_count", len(workflowDSL.Edges)),
	)

	plan, err := builder.Build(workflowDSL)
	if err != nil {
		log.Warn("run stage: build plan failed", zap.Error(err))
		return e.failRun(ctx, runID, startedAt, err)
	}
	log.Info("run stage: plan built",
		zap.Int("plan_nodes", len(plan.Nodes)),
		zap.Int("end_nodes", len(plan.EndIDs)),
		zap.Int("concurrency", e.Concurrency),
	)

	pool := varpool.New()
	result, runErr := scheduler.Run(ctx, plan, pool, scheduler.Options{
		RunID:       runID,
		Input:       input,
		Concurrency: e.Concurrency,
		Registry:    e.registry,
	})
	if runErr != nil {
		log.Warn("run stage: scheduling finished with error",
			zap.Duration("elapsed", time.Since(startedAt)), zap.Error(runErr))
	} else {
		log.Info("run stage: scheduling succeeded",
			zap.Duration("elapsed", time.Since(startedAt)),
			zap.Int("executed_nodes", nodeCount(result)),
		)
	}

	if err := e.persistFinal(ctx, runID, startedAt, result, runErr); err != nil {
		log.Error("run stage: persist final failed", zap.Error(err))
		if runErr != nil {
			return fmt.Errorf("%w; additionally failed to persist run result: %v", runErr, err)
		}
		return err
	}
	log.Info("run stage: final persisted",
		zap.String("final_status", finalStatus(runErr)))
	return runErr
}

// nodeCount 安全统计调度结果的节点数（result 可能为 nil）。
func nodeCount(r *scheduler.Result) int {
	if r == nil {
		return 0
	}
	return len(r.Nodes)
}

// finalStatus 把 runErr 归一为落库的终态字符串（供日志展示）。
func finalStatus(runErr error) string {
	if runErr != nil {
		return RunStatusFailed
	}
	return RunStatusSucceeded
}

func (e *Engine) loadDSL(ctx context.Context, workflowID string, version int32) (*dsl.DSL, error) {
	var raw json.RawMessage
	if version == DraftVersion {
		wf, err := e.store.Q.GetWorkflow(ctx, workflowID)
		if err != nil {
			return nil, fmt.Errorf("engine: get workflow draft %s: %w", workflowID, err)
		}
		raw = wf.DraftDsl
	} else {
		ver, err := e.store.Q.GetWorkflowVersion(ctx, gen.GetWorkflowVersionParams{
			WorkflowID: workflowID,
			Version:    version,
		})
		if err != nil {
			return nil, fmt.Errorf("engine: get workflow %s version %d: %w", workflowID, version, err)
		}
		raw = ver.Dsl
	}

	if len(raw) == 0 || string(raw) == "{}" || string(raw) == "null" {
		return &dsl.DSL{}, nil
	}
	var out dsl.DSL
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("engine: unmarshal dsl: %w", err)
	}
	return &out, nil
}

func (e *Engine) failRun(ctx context.Context, runID string, startedAt time.Time, runErr error) error {
	if err := e.persistFinal(ctx, runID, startedAt, nil, runErr); err != nil {
		return fmt.Errorf("%w; additionally failed to persist run failure: %v", runErr, err)
	}
	return runErr
}

func (e *Engine) persistFinal(ctx context.Context, runID string, startedAt time.Time, result *scheduler.Result, runErr error) error {
	finishedAt := time.Now()
	return e.store.WithTx(func(q *gen.Queries) error {
		if result != nil {
			for _, n := range result.Nodes {
				if _, err := q.CreateNodeRun(ctx, gen.CreateNodeRunParams{
					RunID:      runID,
					NodeID:     n.NodeID,
					NodeType:   n.NodeType,
					Status:     n.Status,
					Input:      mustJSON(n.Input),
					Output:     mustJSON(n.Output),
					Error:      nullString(n.Err),
					Attempt:    int32(n.Attempt),
					CostMs:     int32(n.Cost.Milliseconds()),
					StartedAt:  nullTime(n.StartedAt),
					FinishedAt: nullTime(n.FinishedAt),
				}); err != nil {
					return fmt.Errorf("engine: create node_run %s: %w", n.NodeID, err)
				}
			}
		}

		status := RunStatusSucceeded
		errMsg := ""
		if runErr != nil {
			status = RunStatusFailed
			errMsg = runErr.Error()
		}
		var outputs any = map[string]any{}
		if result != nil {
			outputs = result.Outputs
		}
		if _, err := q.UpdateRunStatus(ctx, gen.UpdateRunStatusParams{
			Status:     status,
			Output:     mustJSON(outputs),
			Error:      nullString(errMsg),
			StartedAt:  nullTime(startedAt),
			FinishedAt: nullTime(finishedAt),
			RunID:      runID,
		}); err != nil {
			return fmt.Errorf("engine: update run status: %w", err)
		}
		return nil
	})
}

func (e *Engine) updateRun(ctx context.Context, runID, status string, output any, errMsg string, startedAt, finishedAt time.Time) error {
	_, err := e.store.Q.UpdateRunStatus(ctx, gen.UpdateRunStatusParams{
		Status:     status,
		Output:     mustJSON(output),
		Error:      nullString(errMsg),
		StartedAt:  nullTime(startedAt),
		FinishedAt: nullTime(finishedAt),
		RunID:      runID,
	})
	return err
}

// FailRunWithError 给一个已创建的 run 打上 failed 墓碑，用于「引擎从未真正跑起来」
// 的边界（后台执行 panic、调度器满载拒绝等）——保证 run 不会永久卡在
// pending/running。保留已有 started_at（读不到则不设），补 finished_at=now。
//
// 与 Run 内部的 persistFinal/failRun 互补：那两条覆盖「跑起来后失败」，
// 本方法覆盖「压根没进 Run 主体或中途崩了」的兜底。
func (e *Engine) FailRunWithError(ctx context.Context, runID, errMsg string) error {
	if e == nil || e.store == nil {
		return fmt.Errorf("engine: nil store")
	}
	e.log().Warn("run stage: force-fail (tombstone)",
		zap.String("run_id", runID), zap.String("reason", errMsg))
	var startedAt time.Time
	if run, err := e.store.Q.GetWorkflowRun(ctx, runID); err == nil && run.StartedAt.Valid {
		startedAt = run.StartedAt.Time
	}
	return e.updateRun(ctx, runID, RunStatusFailed, map[string]any{}, errMsg, startedAt, time.Now())
}

func decodeInput(raw json.RawMessage) (map[string]any, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return map[string]any{}, nil
	}
	out := map[string]any{}
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func mustJSON(v any) json.RawMessage {
	if v == nil {
		return json.RawMessage("null")
	}
	b, err := json.Marshal(v)
	if err != nil {
		// map[string]any / scalar 输入不应失败；保底返回 JSON null，避免写入非法 JSON。
		return json.RawMessage("null")
	}
	return b
}

func nullString(s string) sql.NullString {
	return sql.NullString{String: s, Valid: s != ""}
}

func nullTime(t time.Time) sql.NullTime {
	return sql.NullTime{Time: t, Valid: !t.IsZero()}
}
