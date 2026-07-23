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

// ErrDatasetRowsNotArray 表示 row_data 不是 JSON 数组（评测集必须是行的数组）。
var ErrDatasetRowsNotArray = errors.New("dataset rows must be a JSON array of objects")

// DatasetService 封装评测集（dataset）的持久化操作（M10）。
// row_data 整存 JSON 数组，每元素一条样本；col_schema 可选记录行字段定义。
type DatasetService struct {
	store *mysql.Store
}

func NewDatasetService(store *mysql.Store) *DatasetService {
	return &DatasetService{store: store}
}

// Dataset 是对外的评测集视图（含行数据）。
type Dataset struct {
	DatasetID string          `json:"dataset_id"`
	ProjectID string          `json:"project_id"`
	Name      string          `json:"name"`
	Schema    json.RawMessage `json:"schema,omitempty"`
	Rows      json.RawMessage `json:"rows,omitempty"`
	RowCount  int32           `json:"row_count"`
	Status    int8            `json:"status"`
}

// DatasetSummary 是列表视图（不含行数据，只给行数）。
type DatasetSummary struct {
	DatasetID string `json:"dataset_id"`
	ProjectID string `json:"project_id"`
	Name      string `json:"name"`
	RowCount  int32  `json:"row_count"`
	Status    int8   `json:"status"`
}

// Create 新建评测集，返回 dataset_id。rows 必须是合法 JSON 数组，row_count 由行数派生。
// schema 若非空须为合法 JSON。
func (s *DatasetService) Create(ctx context.Context, projectID, name string, schema, rows json.RawMessage) (string, error) {
	rowData, n, err := normalizeRows(rows)
	if err != nil {
		return "", err
	}
	sch, err := normalizeJSON(schema)
	if err != nil {
		return "", err
	}

	datasetID := genID("ds")
	if _, err := s.store.Q.CreateDataset(ctx, gen.CreateDatasetParams{
		DatasetID: datasetID,
		ProjectID: projectID,
		Name:      name,
		ColSchema: sch,
		RowData:   rowData,
		RowCount:  n,
	}); err != nil {
		return "", fmt.Errorf("create dataset: %w", err)
	}
	return datasetID, nil
}

// Get 读取评测集（含行数据）。
func (s *DatasetService) Get(ctx context.Context, datasetID string) (*Dataset, error) {
	row, err := s.store.Q.GetDataset(ctx, datasetID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &Dataset{
		DatasetID: row.DatasetID,
		ProjectID: row.ProjectID,
		Name:      row.Name,
		Schema:    row.ColSchema,
		Rows:      row.RowData,
		RowCount:  row.RowCount,
		Status:    row.Status,
	}, nil
}

// List 按项目分页列出评测集（不含行数据）。
func (s *DatasetService) List(ctx context.Context, projectID string, limit, offset int32) ([]DatasetSummary, error) {
	rows, err := s.store.Q.ListDatasets(ctx, gen.ListDatasetsParams{
		ProjectID: projectID,
		Limit:     limit,
		Offset:    offset,
	})
	if err != nil {
		return nil, fmt.Errorf("list datasets: %w", err)
	}
	out := make([]DatasetSummary, 0, len(rows))
	for _, r := range rows {
		out = append(out, DatasetSummary{
			DatasetID: r.DatasetID,
			ProjectID: r.ProjectID,
			Name:      r.Name,
			RowCount:  r.RowCount,
			Status:    r.Status,
		})
	}
	return out, nil
}

// Search 按项目 + 名称模糊搜索评测集（M8/M10 能力发现）。name 为空时匹配全部。
func (s *DatasetService) Search(ctx context.Context, projectID, name string, limit, offset int32) ([]DatasetSummary, error) {
	rows, err := s.store.Q.SearchDatasets(ctx, gen.SearchDatasetsParams{
		ProjectID: projectID,
		Name:      likePattern(name),
		Limit:     limit,
		Offset:    offset,
	})
	if err != nil {
		return nil, fmt.Errorf("search datasets: %w", err)
	}
	out := make([]DatasetSummary, 0, len(rows))
	for _, r := range rows {
		out = append(out, DatasetSummary{
			DatasetID: r.DatasetID,
			ProjectID: r.ProjectID,
			Name:      r.Name,
			RowCount:  r.RowCount,
			Status:    r.Status,
		})
	}
	return out, nil
}

// Update 修改评测集 name/schema/rows。rows 若非空须为合法 JSON 数组，并重算 row_count。
func (s *DatasetService) Update(ctx context.Context, datasetID, name string, schema, rows json.RawMessage) error {
	rowData, n, err := normalizeRows(rows)
	if err != nil {
		return err
	}
	sch, err := normalizeJSON(schema)
	if err != nil {
		return err
	}

	res, err := s.store.Q.UpdateDataset(ctx, gen.UpdateDatasetParams{
		Name:      name,
		ColSchema: sch,
		RowData:   rowData,
		RowCount:  n,
		DatasetID: datasetID,
	})
	if err != nil {
		return fmt.Errorf("update dataset: %w", err)
	}
	if affected, _ := res.RowsAffected(); affected == 0 {
		return ErrNotFound
	}
	return nil
}

// Delete 软删除评测集。
func (s *DatasetService) Delete(ctx context.Context, datasetID string) error {
	res, err := s.store.Q.SoftDeleteDataset(ctx, datasetID)
	if err != nil {
		return fmt.Errorf("delete dataset: %w", err)
	}
	if affected, _ := res.RowsAffected(); affected == 0 {
		return ErrNotFound
	}
	return nil
}

// normalizeRows 校验 rows 为合法 JSON 数组，返回归一化后的字节与行数。
// 空输入归一为 "[]"（0 行），非数组报 ErrDatasetRowsNotArray。
func normalizeRows(rows json.RawMessage) (json.RawMessage, int32, error) {
	if len(rows) == 0 || string(rows) == "null" {
		return json.RawMessage("[]"), 0, nil
	}
	var arr []json.RawMessage
	if err := json.Unmarshal(rows, &arr); err != nil {
		return nil, 0, ErrDatasetRowsNotArray
	}
	return rows, int32(len(arr)), nil
}
