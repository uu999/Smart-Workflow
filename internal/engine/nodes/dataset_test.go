package nodes

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/smart-workflow/smart-workflow/internal/dsl"
)

// mockDatasetResolver 是可注入的假解析器。
type mockDatasetResolver struct {
	rows []map[string]any
	err  error
}

func (m mockDatasetResolver) ResolveDataset(_ context.Context, _ string) ([]map[string]any, error) {
	return m.rows, m.err
}

func datasetNode(datasetID string) dsl.DSLNode {
	p := map[string]any{}
	if datasetID != "" {
		p["datasetId"] = datasetID
	}
	return dsl.DSLNode{
		ID:   "dataset::x",
		Data: dsl.NodeData{NodeMeta: dsl.NodeMeta{NodeType: dsl.KindDataset}, NodeParam: p},
	}
}

func TestDatasetExecutor_OutputsRows(t *testing.T) {
	exec := DatasetExecutor{Resolver: mockDatasetResolver{rows: []map[string]any{
		{"query": "好评", "label": "正面"},
		{"query": "差评", "label": "负面"},
	}}}
	res, err := exec.Execute(context.Background(), &ExecContext{Node: datasetNode("ds_1")})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Outputs["count"] != 2 {
		t.Fatalf("count = %v, want 2", res.Outputs["count"])
	}
	rows, ok := res.Outputs["rows"].([]any)
	if !ok || len(rows) != 2 {
		t.Fatalf("rows not []any of len 2: %T %v", res.Outputs["rows"], res.Outputs["rows"])
	}
	first, _ := rows[0].(map[string]any)
	if first["label"] != "正面" {
		t.Fatalf("row content mismatch: %+v", rows[0])
	}
}

func TestDatasetExecutor_EmptyRows(t *testing.T) {
	exec := DatasetExecutor{Resolver: mockDatasetResolver{rows: nil}}
	res, err := exec.Execute(context.Background(), &ExecContext{Node: datasetNode("ds_empty")})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Outputs["count"] != 0 {
		t.Fatalf("count = %v, want 0", res.Outputs["count"])
	}
	if rows, ok := res.Outputs["rows"].([]any); !ok || len(rows) != 0 {
		t.Fatalf("rows should be empty []any, got %T %v", res.Outputs["rows"], res.Outputs["rows"])
	}
}

func TestDatasetExecutor_MissingDatasetID(t *testing.T) {
	exec := DatasetExecutor{Resolver: mockDatasetResolver{}}
	_, err := exec.Execute(context.Background(), &ExecContext{Node: datasetNode("")})
	if err == nil || !strings.Contains(err.Error(), "missing dataset_id") {
		t.Fatalf("expected missing dataset_id error, got: %v", err)
	}
}

func TestDatasetExecutor_NilResolver(t *testing.T) {
	exec := DatasetExecutor{}
	_, err := exec.Execute(context.Background(), &ExecContext{Node: datasetNode("ds_1")})
	if err == nil || !strings.Contains(err.Error(), "no dataset resolver") {
		t.Fatalf("expected no resolver error, got: %v", err)
	}
}

func TestDatasetExecutor_ResolverError(t *testing.T) {
	exec := DatasetExecutor{Resolver: mockDatasetResolver{err: errors.New("db down")}}
	_, err := exec.Execute(context.Background(), &ExecContext{Node: datasetNode("ds_1")})
	if err == nil || !strings.Contains(err.Error(), "db down") {
		t.Fatalf("expected resolver error propagated, got: %v", err)
	}
}

// TestDatasetExecutor_RegisteredOnlyWithResolver 守卫「nil resolver 不注册」的零影响约定。
func TestDatasetExecutor_RegisteredOnlyWithResolver(t *testing.T) {
	// 无 resolver：不应注册 dataset/application。
	bare := NewDefaultRegistry(Config{})
	if _, ok := bare.Get(dsl.KindDataset); ok {
		t.Fatal("dataset executor should NOT be registered without resolver")
	}
	if _, ok := bare.Get(dsl.KindApplication); ok {
		t.Fatal("application executor should NOT be registered without resolver")
	}
	// 注入 resolver：应注册。
	withRes := NewDefaultRegistry(Config{
		DatasetResolver: mockDatasetResolver{},
		AppResolver:     mockAppResolver{},
	})
	if _, ok := withRes.Get(dsl.KindDataset); !ok {
		t.Fatal("dataset executor should be registered with resolver")
	}
	if _, ok := withRes.Get(dsl.KindApplication); !ok {
		t.Fatal("application executor should be registered with resolver")
	}
}
