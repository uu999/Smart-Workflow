package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/smart-workflow/smart-workflow/internal/dsl"
	"github.com/smart-workflow/smart-workflow/internal/storage/mysql"
	"github.com/smart-workflow/smart-workflow/internal/storage/mysql/gen"
	"github.com/smart-workflow/smart-workflow/internal/validator"
)

// DraftVersion 表示以草稿 DSL 调试运行（对齐 engine.DraftVersion）。
const DraftVersion int32 = -1

// runStatusPending 是新建 run 的初始状态（对齐 engine.RunStatusPending）。
const runStatusPending = "pending"

// ErrInvalidVersion 表示显式传入的 version 语义非法（如 0 或 < -1 的负数）。
// version 语义：省略(nil)=最新发布版本 / -1=草稿 / N>0=指定版本。
var ErrInvalidVersion = errors.New("invalid version: omit for latest published, -1 for draft, or a positive version number")

// ValidationError 表示 run 前置校验（TD-10 gate）未通过：图有 error 级问题，
// 拒绝创建 run，携带结构化 issues 供 CLI/Agent 定位并自动修复，
// 而非让坏图一路跑进 builder 落 failed 后只拿到裸字符串错误。
type ValidationError struct {
	Issues []validator.Issue
}

func (e *ValidationError) Error() string {
	return fmt.Sprintf("workflow validation failed with %d error(s); fix them before running", errorCount(e.Issues))
}

// errorCount 统计 error 级问题数（供错误消息用）。
func errorCount(issues []validator.Issue) int {
	n := 0
	for _, i := range issues {
		if i.Severity == validator.SeverityError {
			n++
		}
	}
	return n
}

// RunService 封装工作流运行记录的操作（M6）。
// 只负责落 pending 记录与读取，真正执行由 engine.Run 完成。
type RunService struct {
	store *mysql.Store
	wf    *WorkflowService
}

func NewRunService(store *mysql.Store) *RunService {
	return &RunService{store: store, wf: NewWorkflowService(store)}
}

// NodeRunView 是单节点执行快照的对外视图。
type NodeRunView struct {
	NodeID   string          `json:"node_id"`
	NodeType string          `json:"node_type"`
	Status   string          `json:"status"`
	Input    json.RawMessage `json:"input,omitempty"`
	Output   json.RawMessage `json:"output,omitempty"`
	Error    string          `json:"error,omitempty"`
	Attempt  int32           `json:"attempt"`
	CostMs   int32           `json:"cost_ms"`
}

// RunView 是运行记录的对外视图（含节点快照）。
type RunView struct {
	RunID       string          `json:"run_id"`
	WorkflowID  string          `json:"workflow_id"`
	Version     int32           `json:"version"`
	Status      string          `json:"status"`
	TriggerType string          `json:"trigger_type"`
	Input       json.RawMessage `json:"input,omitempty"`
	Output      json.RawMessage `json:"output,omitempty"`
	Error       string          `json:"error,omitempty"`
	Nodes       []NodeRunView   `json:"nodes,omitempty"`
}

// RunSummary 是运行列表视图。
type RunSummary struct {
	RunID       string `json:"run_id"`
	WorkflowID  string `json:"workflow_id"`
	Version     int32  `json:"version"`
	Status      string `json:"status"`
	TriggerType string `json:"trigger_type"`
}

// CreateRun 落一条 pending 运行记录并返回 runID。
// version 语义（消歧硬伤2：用指针区分「省略」与「显式传值」）：
//
//	nil（省略） → 取 workflow.published_ver（未发布则报错）
//	-1          → 草稿调试（对齐 engine.DraftVersion）
//	N > 0       → 指定已发布版本
//	0 或 < -1   → ErrInvalidVersion（不再被静默当成「最新发布」）
//
// 真正执行由调用方拿 runID 交给 engine.Run（M6 走后台 dispatcher）。
func (s *RunService) CreateRun(ctx context.Context, workflowID string, version *int32, input json.RawMessage, trigger string) (string, error) {
	resolved, err := s.resolveVersion(ctx, workflowID, version)
	if err != nil {
		return "", err
	}

	// TD-10 run gate：执行前先静态校验目标 DSL，坏图（缺 start/环/坏 ref 等）
	// 直接拒绝并回结构化 issues，不落 pending、不进 builder/scheduler。
	// 对齐 PaiFlow "validate 过才 run" 的心智，也让 CLI/Agent 拿到可定位反馈。
	if verr := s.validateForRun(ctx, workflowID, resolved); verr != nil {
		return "", verr
	}

	if trigger == "" {
		trigger = "api"
	}
	if len(input) == 0 {
		input = json.RawMessage("{}")
	} else if !json.Valid(input) {
		return "", ErrInvalidJSON
	}

	runID := genID("run")
	if _, err := s.store.Q.CreateWorkflowRun(ctx, gen.CreateWorkflowRunParams{
		RunID:       runID,
		WorkflowID:  workflowID,
		Version:     resolved,
		Status:      runStatusPending,
		TriggerType: trigger,
		Input:       input,
	}); err != nil {
		return "", fmt.Errorf("create run: %w", err)
	}
	return runID, nil
}

// validateForRun 加载 resolved 版本对应的 DSL 并做静态校验（TD-10 gate）。
// 有 error 级问题时返回 *ValidationError（携带全部 issues）；否则返回 nil。
// 找不到工作流/版本等加载错误按原样返回（映射为 404 等），不吞成校验错误。
func (s *RunService) validateForRun(ctx context.Context, workflowID string, resolved int32) error {
	var (
		d   *dsl.DSL
		err error
	)
	if resolved == DraftVersion {
		var wf *Workflow
		wf, err = s.wf.Get(ctx, workflowID)
		if err == nil {
			d = wf.Draft
		}
	} else {
		d, err = s.wf.GetVersion(ctx, workflowID, resolved)
	}
	if err != nil {
		return err
	}
	res := validateDSL(d, dsl.Meta{})
	if res.HasError() {
		return &ValidationError{Issues: res.Issues}
	}
	return nil
}

// resolveVersion 把外部传入的 version 归一为落库值。
// nil=省略（取已发布）；否则按显式值校验语义。
func (s *RunService) resolveVersion(ctx context.Context, workflowID string, version *int32) (int32, error) {
	if version != nil {
		switch v := *version; {
		case v == DraftVersion:
			return DraftVersion, nil
		case v > 0:
			return v, nil
		default:
			// 显式传 0 或 < -1：语义非法，明确报错而非静默当「最新发布」。
			return 0, ErrInvalidVersion
		}
	}
	// 省略：取已发布版本。
	wf, err := s.store.Q.GetWorkflow(ctx, workflowID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, ErrNotFound
		}
		return 0, err
	}
	if wf.PublishedVer <= 0 {
		return 0, fmt.Errorf("workflow %s has no published version; pass version=-1 to run draft", workflowID)
	}
	return wf.PublishedVer, nil
}

// GetRun 读取运行记录及其节点快照。
func (s *RunService) GetRun(ctx context.Context, runID string) (*RunView, error) {
	run, err := s.store.Q.GetWorkflowRun(ctx, runID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	nodeRuns, err := s.store.Q.ListNodeRuns(ctx, runID)
	if err != nil {
		return nil, fmt.Errorf("list node runs: %w", err)
	}
	view := &RunView{
		RunID:       run.RunID,
		WorkflowID:  run.WorkflowID,
		Version:     run.Version,
		Status:      run.Status,
		TriggerType: run.TriggerType,
		Input:       run.Input,
		Output:      run.Output,
		Error:       run.Error.String,
		Nodes:       make([]NodeRunView, 0, len(nodeRuns)),
	}
	for _, n := range nodeRuns {
		view.Nodes = append(view.Nodes, NodeRunView{
			NodeID:   n.NodeID,
			NodeType: n.NodeType,
			Status:   n.Status,
			Input:    n.Input,
			Output:   n.Output,
			Error:    n.Error.String,
			Attempt:  n.Attempt,
			CostMs:   n.CostMs,
		})
	}
	return view, nil
}

// ListRuns 按工作流分页列出运行。
func (s *RunService) ListRuns(ctx context.Context, workflowID string, limit, offset int32) ([]RunSummary, error) {
	rows, err := s.store.Q.ListRunsByWorkflow(ctx, gen.ListRunsByWorkflowParams{
		WorkflowID: workflowID,
		Limit:      limit,
		Offset:     offset,
	})
	if err != nil {
		return nil, fmt.Errorf("list runs: %w", err)
	}
	out := make([]RunSummary, 0, len(rows))
	for _, r := range rows {
		out = append(out, RunSummary{
			RunID:       r.RunID,
			WorkflowID:  r.WorkflowID,
			Version:     r.Version,
			Status:      r.Status,
			TriggerType: r.TriggerType,
		})
	}
	return out, nil
}
