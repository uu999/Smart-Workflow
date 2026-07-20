package service

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/smart-workflow/smart-workflow/internal/dsl"
	"github.com/smart-workflow/smart-workflow/internal/storage/mysql"
	"github.com/smart-workflow/smart-workflow/internal/storage/mysql/gen"
)

// 常见错误。
var (
	ErrNotFound      = errors.New("workflow not found")
	ErrVersionLock   = errors.New("version conflict: workflow was modified by someone else")
	ErrAlreadyExists = errors.New("workflow already exists")
)

// WorkflowService 封装工作流的持久化操作（M2）。
type WorkflowService struct {
	store *mysql.Store
}

func NewWorkflowService(store *mysql.Store) *WorkflowService {
	return &WorkflowService{store: store}
}

// Workflow 是对外的工作流视图。
type Workflow struct {
	WorkflowID   string   `json:"workflow_id"`
	ProjectID    string   `json:"project_id"`
	Name         string   `json:"name"`
	Description  string   `json:"description"`
	Draft        *dsl.DSL `json:"draft,omitempty"`
	PublishedVer int32    `json:"published_ver"`
	Status       int8     `json:"status"`
	VersionLock  int32    `json:"version_lock"`
}

// Create 新建工作流，draft 为初始 DSL（可空，空则存 {}）。
func (s *WorkflowService) Create(ctx context.Context, projectID, name, description string, draft *dsl.DSL) (string, error) {
	wfID := genID("wf")
	raw, err := marshalDSL(draft)
	if err != nil {
		return "", err
	}
	_, err = s.store.Q.CreateWorkflow(ctx, gen.CreateWorkflowParams{
		WorkflowID:  wfID,
		ProjectID:   projectID,
		Name:        name,
		Description: description,
		DraftDsl:    raw,
	})
	if err != nil {
		return "", fmt.Errorf("create workflow: %w", err)
	}
	return wfID, nil
}

// Get 返回工作流（含草稿 DSL）。
func (s *WorkflowService) Get(ctx context.Context, workflowID string) (*Workflow, error) {
	row, err := s.store.Q.GetWorkflow(ctx, workflowID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	draft, err := unmarshalDSL(row.DraftDsl)
	if err != nil {
		return nil, err
	}
	return &Workflow{
		WorkflowID:   row.WorkflowID,
		ProjectID:    row.ProjectID,
		Name:         row.Name,
		Description:  row.Description,
		Draft:        draft,
		PublishedVer: row.PublishedVer,
		Status:       row.Status,
		VersionLock:  row.VersionLock,
	}, nil
}

// UpdateDraft 用乐观锁更新草稿。expectLock 必须等于当前 version_lock，否则冲突。
func (s *WorkflowService) UpdateDraft(ctx context.Context, workflowID, name, description string, draft *dsl.DSL, expectLock int32) error {
	raw, err := marshalDSL(draft)
	if err != nil {
		return err
	}
	res, err := s.store.Q.UpdateWorkflowDraft(ctx, gen.UpdateWorkflowDraftParams{
		Name:        name,
		Description: description,
		DraftDsl:    raw,
		WorkflowID:  workflowID,
		VersionLock: expectLock,
	})
	if err != nil {
		return fmt.Errorf("update draft: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrVersionLock // 锁不匹配或已删除
	}
	return nil
}

// Publish 把当前草稿冻结为新 version 快照，并更新 published_ver。事务保证一致。
func (s *WorkflowService) Publish(ctx context.Context, workflowID, changeLog string) (int32, error) {
	var newVer int32
	err := s.store.WithTx(func(q *gen.Queries) error {
		wf, err := q.GetWorkflow(ctx, workflowID)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return ErrNotFound
			}
			return err
		}
		maxVerRaw, err := q.MaxWorkflowVersion(ctx, workflowID)
		if err != nil {
			return err
		}
		newVer = toInt32(maxVerRaw) + 1
		if _, err := q.CreateWorkflowVersion(ctx, gen.CreateWorkflowVersionParams{
			WorkflowID: workflowID,
			Version:    newVer,
			Dsl:        wf.DraftDsl, // 冻结当前草稿
			ChangeLog:  changeLog,
		}); err != nil {
			return err
		}
		if _, err := q.PublishWorkflow(ctx, gen.PublishWorkflowParams{
			PublishedVer: newVer,
			WorkflowID:   workflowID,
		}); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	return newVer, nil
}

// GetVersion 读取某个发布版本的不可变 DSL。
func (s *WorkflowService) GetVersion(ctx context.Context, workflowID string, version int32) (*dsl.DSL, error) {
	row, err := s.store.Q.GetWorkflowVersion(ctx, gen.GetWorkflowVersionParams{
		WorkflowID: workflowID,
		Version:    version,
	})
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return unmarshalDSL(row.Dsl)
}

// WorkflowSummary 是列表视图（不含完整 DSL，避免大对象）。
type WorkflowSummary struct {
	WorkflowID   string `json:"workflow_id"`
	ProjectID    string `json:"project_id"`
	Name         string `json:"name"`
	Description  string `json:"description"`
	PublishedVer int32  `json:"published_ver"`
	Status       int8   `json:"status"`
}

// List 按项目分页列出工作流（不含草稿 DSL）。
func (s *WorkflowService) List(ctx context.Context, projectID string, limit, offset int32) ([]WorkflowSummary, error) {
	rows, err := s.store.Q.ListWorkflows(ctx, gen.ListWorkflowsParams{
		ProjectID: projectID,
		Limit:     limit,
		Offset:    offset,
	})
	if err != nil {
		return nil, fmt.Errorf("list workflows: %w", err)
	}
	out := make([]WorkflowSummary, 0, len(rows))
	for _, r := range rows {
		out = append(out, WorkflowSummary{
			WorkflowID:   r.WorkflowID,
			ProjectID:    r.ProjectID,
			Name:         r.Name,
			Description:  r.Description,
			PublishedVer: r.PublishedVer,
			Status:       r.Status,
		})
	}
	return out, nil
}

// Delete 软删除工作流。
func (s *WorkflowService) Delete(ctx context.Context, workflowID string) error {
	res, err := s.store.Q.SoftDeleteWorkflow(ctx, workflowID)
	if err != nil {
		return fmt.Errorf("delete workflow: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// --- helpers ---

func marshalDSL(d *dsl.DSL) (json.RawMessage, error) {
	if d == nil {
		return json.RawMessage("{}"), nil
	}
	b, err := json.Marshal(d)
	if err != nil {
		return nil, fmt.Errorf("marshal dsl: %w", err)
	}
	return b, nil
}

func unmarshalDSL(raw json.RawMessage) (*dsl.DSL, error) {
	if len(raw) == 0 || string(raw) == "{}" || string(raw) == "null" {
		return &dsl.DSL{}, nil
	}
	var d dsl.DSL
	if err := json.Unmarshal(raw, &d); err != nil {
		return nil, fmt.Errorf("unmarshal dsl: %w", err)
	}
	return &d, nil
}

func genID(prefix string) string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return prefix + "_" + hex.EncodeToString(b)
}

// toInt32 把 sqlc 对 COALESCE(MAX(...)) 推断出的 interface{} 安全转成 int32。
// MySQL 驱动通常返回 int64；也兼容 []byte / float64 等形态。
func toInt32(v any) int32 {
	switch n := v.(type) {
	case int64:
		return int32(n)
	case int32:
		return n
	case int:
		return int32(n)
	case float64:
		return int32(n)
	case []byte:
		var x int64
		_, _ = fmt.Sscanf(string(n), "%d", &x)
		return int32(x)
	default:
		return 0
	}
}
