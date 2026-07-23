package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/smart-workflow/smart-workflow/internal/storage/mysql"
	"github.com/smart-workflow/smart-workflow/internal/storage/mysql/gen"
)

// ErrInvalidJSON 表示传入的 schema/config 不是合法 JSON。
var ErrInvalidJSON = errors.New("invalid json in schema/config field")

// ApplicationService 封装应用（能力）的持久化操作（M6）。
type ApplicationService struct {
	store *mysql.Store
}

func NewApplicationService(store *mysql.Store) *ApplicationService {
	return &ApplicationService{store: store}
}

// Application 是对外的应用视图。
type Application struct {
	AppID        string          `json:"app_id"`
	ProjectID    string          `json:"project_id"`
	Name         string          `json:"name"`
	Kind         string          `json:"kind"` // http/python/rpc
	InputSchema  json.RawMessage `json:"input_schema,omitempty"`
	OutputSchema json.RawMessage `json:"output_schema,omitempty"`
	Config       json.RawMessage `json:"config,omitempty"`
	Status       int8            `json:"status"`
}

// ApplicationSummary 是列表视图（不含 schema/config）。
type ApplicationSummary struct {
	AppID     string `json:"app_id"`
	ProjectID string `json:"project_id"`
	Name      string `json:"name"`
	Kind      string `json:"kind"`
	Status    int8   `json:"status"`
}

// Create 新建应用，返回 app_id。input_schema/output_schema/config 若非空须为合法 JSON。
func (s *ApplicationService) Create(ctx context.Context, projectID, name, kind string, inputSchema, outputSchema, config json.RawMessage) (string, error) {
	in, err := normalizeJSON(inputSchema)
	if err != nil {
		return "", err
	}
	out, err := normalizeJSON(outputSchema)
	if err != nil {
		return "", err
	}
	cfg, err := normalizeJSON(config)
	if err != nil {
		return "", err
	}

	appID := genID("app")
	if _, err := s.store.Q.CreateApplication(ctx, gen.CreateApplicationParams{
		AppID:        appID,
		ProjectID:    projectID,
		Name:         name,
		Kind:         kind,
		InputSchema:  in,
		OutputSchema: out,
		Config:       cfg,
	}); err != nil {
		return "", fmt.Errorf("create application: %w", err)
	}
	return appID, nil
}

// Get 读取应用（含 schema/config）。
func (s *ApplicationService) Get(ctx context.Context, appID string) (*Application, error) {
	row, err := s.store.Q.GetApplication(ctx, appID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &Application{
		AppID:        row.AppID,
		ProjectID:    row.ProjectID,
		Name:         row.Name,
		Kind:         row.Kind,
		InputSchema:  row.InputSchema,
		OutputSchema: row.OutputSchema,
		Config:       row.Config,
		Status:       row.Status,
	}, nil
}

// List 按项目分页列出应用（不含 schema/config）。
func (s *ApplicationService) List(ctx context.Context, projectID string, limit, offset int32) ([]ApplicationSummary, error) {
	rows, err := s.store.Q.ListApplications(ctx, gen.ListApplicationsParams{
		ProjectID: projectID,
		Limit:     limit,
		Offset:    offset,
	})
	if err != nil {
		return nil, fmt.Errorf("list applications: %w", err)
	}
	out := make([]ApplicationSummary, 0, len(rows))
	for _, r := range rows {
		out = append(out, ApplicationSummary{
			AppID:     r.AppID,
			ProjectID: r.ProjectID,
			Name:      r.Name,
			Kind:      r.Kind,
			Status:    r.Status,
		})
	}
	return out, nil
}

// Search 按项目 + 名称模糊搜索应用（M8 能力发现）。name 为空时匹配全部。
func (s *ApplicationService) Search(ctx context.Context, projectID, name string, limit, offset int32) ([]ApplicationSummary, error) {
	rows, err := s.store.Q.SearchApplications(ctx, gen.SearchApplicationsParams{
		ProjectID: projectID,
		Name:      likePattern(name),
		Limit:     limit,
		Offset:    offset,
	})
	if err != nil {
		return nil, fmt.Errorf("search applications: %w", err)
	}
	out := make([]ApplicationSummary, 0, len(rows))
	for _, r := range rows {
		out = append(out, ApplicationSummary{
			AppID:     r.AppID,
			ProjectID: r.ProjectID,
			Name:      r.Name,
			Kind:      r.Kind,
			Status:    r.Status,
		})
	}
	return out, nil
}

// Update 修改应用 name/kind/schema/config。schema/config 若非空须为合法 JSON。
func (s *ApplicationService) Update(ctx context.Context, appID, name, kind string, inputSchema, outputSchema, config json.RawMessage) error {
	in, err := normalizeJSON(inputSchema)
	if err != nil {
		return err
	}
	out, err := normalizeJSON(outputSchema)
	if err != nil {
		return err
	}
	cfg, err := normalizeJSON(config)
	if err != nil {
		return err
	}

	res, err := s.store.Q.UpdateApplication(ctx, gen.UpdateApplicationParams{
		Name:         name,
		Kind:         kind,
		InputSchema:  in,
		OutputSchema: out,
		Config:       cfg,
		AppID:        appID,
	})
	if err != nil {
		return fmt.Errorf("update application: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// Delete 软删除应用。
func (s *ApplicationService) Delete(ctx context.Context, appID string) error {
	res, err := s.store.Q.SoftDeleteApplication(ctx, appID)
	if err != nil {
		return fmt.Errorf("delete application: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// normalizeJSON 校验并归一化可选 JSON 字段（input_schema/output_schema/config/col_schema）。
// 关键：空输入归一为 JSON 字面量 "null"（而非 Go nil）。原因——这些列是 `JSON NULL`，
// 若存 SQL NULL，读回时 sqlc 生成的 json.RawMessage Scan 目标无法接收 NULL 会报
// "unsupported Scan, storing driver.Value type <nil>"。存合法字面量 null 则列非 SQL NULL，
// 往返安全，且语义等价（JSON null == 无值）。非法 JSON 报 ErrInvalidJSON。
func normalizeJSON(raw json.RawMessage) (json.RawMessage, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return json.RawMessage("null"), nil
	}
	if !json.Valid(raw) {
		return nil, ErrInvalidJSON
	}
	return raw, nil
}

// likePattern 把用户搜索词转成 SQL LIKE 模式：空串→"%"（匹配全部），
// 否则转义 LIKE 元字符（\ % _）后包成 %term%（子串匹配）。
func likePattern(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return "%"
	}
	r := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return "%" + r.Replace(name) + "%"
}
