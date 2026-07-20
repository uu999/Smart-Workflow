package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/smart-workflow/smart-workflow/internal/storage/mysql"
	"github.com/smart-workflow/smart-workflow/internal/storage/mysql/gen"
)

// DraftVersion 表示以草稿 DSL 调试运行（对齐 engine.DraftVersion）。
const DraftVersion int32 = -1

// runStatusPending 是新建 run 的初始状态（对齐 engine.RunStatusPending）。
const runStatusPending = "pending"

// RunService 封装工作流运行记录的操作（M6）。
// 只负责落 pending 记录与读取，真正执行由 engine.Run 完成。
type RunService struct {
	store *mysql.Store
}

func NewRunService(store *mysql.Store) *RunService {
	return &RunService{store: store}
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
// version 语义：0/缺省 → 取 workflow.published_ver（未发布则报错）；-1 → 草稿调试。
// 真正执行由调用方拿 runID 交给 engine.Run（M6 走后台 goroutine）。
func (s *RunService) CreateRun(ctx context.Context, workflowID string, version int32, input json.RawMessage, trigger string) (string, error) {
	resolved, err := s.resolveVersion(ctx, workflowID, version)
	if err != nil {
		return "", err
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

// resolveVersion 把外部传入的 version 归一为落库值。
func (s *RunService) resolveVersion(ctx context.Context, workflowID string, version int32) (int32, error) {
	if version == DraftVersion {
		return DraftVersion, nil
	}
	if version > 0 {
		return version, nil
	}
	// 0/缺省：取已发布版本。
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
