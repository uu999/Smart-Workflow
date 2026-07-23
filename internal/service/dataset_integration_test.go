//go:build integration

package service

import (
	"context"
	"encoding/json"
	"testing"
)

// 运行方式（同 workflow 集成测试）：
//   SWF_TEST_DSN='swf:swfpass@tcp(127.0.0.1:3308)/smart_workflow?parseTime=true' \
//   go test -tags integration ./internal/service/ -run TestM10_Dataset -v
// 需要先 make infra-up + migrate-up（含 00003_dataset）。
//
// 复用 workflow_integration_test.go 的 testStore(t)。

func TestM10_DatasetCRUD(t *testing.T) {
	st := testStore(t)
	defer st.Close()
	svc := NewDatasetService(st)
	ctx := context.Background()

	schema := json.RawMessage(`[{"name":"query","type":"string"},{"name":"label","type":"string"}]`)
	rows := json.RawMessage(`[{"query":"好评","label":"正面"},{"query":"差评","label":"负面"}]`)

	// Create：行数应派生为 2。
	dsID, err := svc.Create(ctx, "proj_test", "情感评测集", schema, rows)
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	// Get：行数据与 schema 应能读回，row_count=2。
	got, err := svc.Get(ctx, dsID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Name != "情感评测集" || got.RowCount != 2 {
		t.Fatalf("get mismatch: name=%q row_count=%d", got.Name, got.RowCount)
	}
	var back []map[string]any
	if err := json.Unmarshal(got.Rows, &back); err != nil || len(back) != 2 {
		t.Fatalf("rows round-trip failed: %v (%s)", err, got.Rows)
	}
	if back[0]["label"] != "正面" {
		t.Fatalf("row content mismatch: %+v", back[0])
	}

	// List / Search 应能找到（不含行数据，但带 row_count）。
	list, err := svc.List(ctx, "proj_test", 50, 0)
	if err != nil || len(list) == 0 {
		t.Fatalf("list: %v (n=%d)", err, len(list))
	}
	found := false
	for _, d := range list {
		if d.DatasetID == dsID {
			found = true
			if d.RowCount != 2 {
				t.Fatalf("list row_count = %d, want 2", d.RowCount)
			}
		}
	}
	if !found {
		t.Fatal("created dataset not in list")
	}
	hits, err := svc.Search(ctx, "proj_test", "情感", 50, 0)
	if err != nil || len(hits) == 0 {
		t.Fatalf("search '情感': %v (n=%d)", err, len(hits))
	}

	// Update：改名 + 追加一行，row_count 应变 3。
	rows3 := json.RawMessage(`[{"query":"好评","label":"正面"},{"query":"差评","label":"负面"},{"query":"一般","label":"中性"}]`)
	if err := svc.Update(ctx, dsID, "情感评测集v2", schema, rows3); err != nil {
		t.Fatalf("update: %v", err)
	}
	after, _ := svc.Get(ctx, dsID)
	if after.Name != "情感评测集v2" || after.RowCount != 3 {
		t.Fatalf("after update mismatch: name=%q row_count=%d", after.Name, after.RowCount)
	}

	// Update 非数组行集应报 ErrDatasetRowsNotArray。
	if err := svc.Update(ctx, dsID, "x", nil, json.RawMessage(`{"not":"array"}`)); err != ErrDatasetRowsNotArray {
		t.Fatalf("expected ErrDatasetRowsNotArray, got %v", err)
	}

	// Delete：软删后 Get 应 ErrNotFound。
	if err := svc.Delete(ctx, dsID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := svc.Get(ctx, dsID); err != ErrNotFound {
		t.Fatalf("expected ErrNotFound after delete, got %v", err)
	}
}
