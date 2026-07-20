package engine

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

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
	Concurrency int
}

// New 用给定 sidecar 地址构造引擎。sidecarURL 通常来自 config.Sidecar.BaseURL，
// 打通了「配置 → code 节点实际调用地址」这条链路。
func New(store *mysql.Store, sidecarURL string) *Engine {
	return &Engine{
		store:       store,
		registry:    nodes.NewDefaultRegistry(nodes.Config{SidecarURL: sidecarURL}),
		Concurrency: 8,
	}
}

// Run 执行一个已创建的 workflow_run：
// 读 run -> 取发布版本/草稿 DSL -> 构建计划 -> 调度执行 -> 写 workflow_run/node_run。
func (e *Engine) Run(ctx context.Context, runID string) error {
	if e == nil || e.store == nil {
		return fmt.Errorf("engine: nil store")
	}

	run, err := e.store.Q.GetWorkflowRun(ctx, runID)
	if err != nil {
		return fmt.Errorf("engine: get run %s: %w", runID, err)
	}

	startedAt := time.Now()
	if err := e.updateRun(ctx, runID, RunStatusRunning, nil, "", startedAt, time.Time{}); err != nil {
		return fmt.Errorf("engine: mark run running: %w", err)
	}

	input, err := decodeInput(run.Input)
	if err != nil {
		return e.failRun(ctx, runID, startedAt, fmt.Errorf("engine: decode run input: %w", err))
	}

	workflowDSL, err := e.loadDSL(ctx, run.WorkflowID, run.Version)
	if err != nil {
		return e.failRun(ctx, runID, startedAt, err)
	}

	plan, err := builder.Build(workflowDSL)
	if err != nil {
		return e.failRun(ctx, runID, startedAt, err)
	}

	pool := varpool.New()
	result, runErr := scheduler.Run(ctx, plan, pool, scheduler.Options{
		RunID:       runID,
		Input:       input,
		Concurrency: e.Concurrency,
		Registry:    e.registry,
	})
	if err := e.persistFinal(ctx, runID, startedAt, result, runErr); err != nil {
		if runErr != nil {
			return fmt.Errorf("%w; additionally failed to persist run result: %v", runErr, err)
		}
		return err
	}
	return runErr
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
