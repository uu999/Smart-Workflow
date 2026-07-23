package engine

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/smart-workflow/smart-workflow/internal/engine/nodes"
	"github.com/smart-workflow/smart-workflow/internal/storage/mysql"
)

// storeResolver 用 *mysql.Store 实现 nodes.AppResolver / nodes.DatasetResolver（M10）。
// 放在 engine 包（而非 service）——service 依赖 engine，若 engine 反向依赖 service 会成环。
// engine 已依赖 mysql，故直接走 store.Q 读取，不引入新依赖方向。
type storeResolver struct {
	store *mysql.Store
}

// 编译期断言：storeResolver 同时满足两个解析接口。
var (
	_ nodes.AppResolver     = (*storeResolver)(nil)
	_ nodes.DatasetResolver = (*storeResolver)(nil)
)

// ResolveApp 按 app_id 读应用行，解出 kind + config（config 列为 JSON 对象）。
func (r *storeResolver) ResolveApp(ctx context.Context, appID string) (*nodes.AppInfo, error) {
	row, err := r.store.Q.GetApplication(ctx, appID)
	if err != nil {
		return nil, fmt.Errorf("resolve app %s: %w", appID, err)
	}
	cfg := map[string]any{}
	if len(row.Config) > 0 && string(row.Config) != "null" {
		if err := json.Unmarshal(row.Config, &cfg); err != nil {
			return nil, fmt.Errorf("resolve app %s: bad config json: %w", appID, err)
		}
	}
	return &nodes.AppInfo{
		AppID:  row.AppID,
		Kind:   row.Kind,
		Name:   row.Name,
		Config: cfg,
	}, nil
}

// ResolveDataset 按 dataset_id 读评测集行集（row_data 列为 JSON 数组）。
func (r *storeResolver) ResolveDataset(ctx context.Context, datasetID string) ([]map[string]any, error) {
	row, err := r.store.Q.GetDataset(ctx, datasetID)
	if err != nil {
		return nil, fmt.Errorf("resolve dataset %s: %w", datasetID, err)
	}
	if len(row.RowData) == 0 || string(row.RowData) == "null" {
		return nil, nil
	}
	var out []map[string]any
	if err := json.Unmarshal(row.RowData, &out); err != nil {
		return nil, fmt.Errorf("resolve dataset %s: bad row_data json: %w", datasetID, err)
	}
	return out, nil
}
