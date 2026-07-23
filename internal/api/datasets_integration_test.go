//go:build integration

package api

import (
	"context"
	"net/http"
	"testing"

	"go.uber.org/zap"

	"github.com/smart-workflow/smart-workflow/internal/config"
	"github.com/smart-workflow/smart-workflow/internal/engine"
)

// 运行方式（需先 make infra-up + migrate-up）：
//
//	SWF_TEST_DSN='swf:swfpass@tcp(127.0.0.1:3308)/smart_workflow?parseTime=true' \
//	go test -tags integration ./internal/api/ -run TestM10_DatasetEndpoints -v
//
// 验收 #36 dataset 接入层：通过 HTTP 走 create→get→list→search→update→delete 全链路，
// 断言 row_count 服务端派生、非数组 rows 报错、软删后不可见。

func TestM10_DatasetEndpoints(t *testing.T) {
	st := e2eTestStore(t)
	defer st.Close()

	eng := engine.New(st, "")
	router := NewRouter(Deps{
		Cfg:    &config.Config{Env: "dev"},
		Logger: zap.NewNop(),
		Store:  st,
		Engine: eng,
	})
	h := &e2eHarness{router: router, store: st}
	ctx := context.Background()

	// 建项目（dataset 挂 project 下）。
	_, env := h.do(t, http.MethodPost, "/v1/projects", map[string]any{"name": "ds-e2e-proj"})
	var projResp struct {
		ProjectID string `json:"project_id"`
	}
	dataInto(t, env, &projResp)
	projID := projResp.ProjectID
	if projID == "" {
		t.Fatal("empty project_id")
	}

	// 建评测集：2 行样本。
	_, env = h.do(t, http.MethodPost, "/v1/datasets", map[string]any{
		"project_id": projID,
		"name":       "情感评测集",
		"rows": []map[string]any{
			{"query": "这家店真棒", "label": "正面"},
			{"query": "太差了", "label": "负面"},
		},
	})
	var createResp struct {
		DatasetID string `json:"dataset_id"`
	}
	dataInto(t, env, &createResp)
	dsID := createResp.DatasetID
	if dsID == "" {
		t.Fatal("empty dataset_id")
	}
	defer func() {
		_, _ = st.DB.ExecContext(ctx, "DELETE FROM dataset WHERE dataset_id = ?", dsID)
		_, _ = st.DB.ExecContext(ctx, "DELETE FROM project WHERE project_id = ?", projID)
	}()

	// Get：row_count 应服务端派生为 2，含行数据。
	_, env = h.do(t, http.MethodGet, "/v1/datasets/"+dsID, nil)
	var getResp struct {
		DatasetID string `json:"dataset_id"`
		Name      string `json:"name"`
		RowCount  int    `json:"row_count"`
		Rows      []map[string]any `json:"rows"`
	}
	dataInto(t, env, &getResp)
	if getResp.RowCount != 2 {
		t.Fatalf("row_count = %d, want 2", getResp.RowCount)
	}
	if len(getResp.Rows) != 2 {
		t.Fatalf("rows len = %d, want 2", len(getResp.Rows))
	}

	// List：项目下应能查到。
	_, env = h.do(t, http.MethodGet, "/v1/datasets?project_id="+projID, nil)
	var listResp struct {
		Items []struct {
			DatasetID string `json:"dataset_id"`
		} `json:"items"`
	}
	dataInto(t, env, &listResp)
	if !containsDS(listResp.Items, dsID) {
		t.Fatalf("list did not contain %s", dsID)
	}

	// Search：按名称模糊匹配。
	_, env = h.do(t, http.MethodGet, "/v1/datasets?project_id="+projID+"&name=情感", nil)
	dataInto(t, env, &listResp)
	if !containsDS(listResp.Items, dsID) {
		t.Fatalf("search by name did not contain %s", dsID)
	}

	// Update：改名 + 换 3 行，row_count 应重算为 3。
	_, env = h.do(t, http.MethodPut, "/v1/datasets/"+dsID, map[string]any{
		"name": "情感评测集v2",
		"rows": []map[string]any{
			{"query": "a", "label": "正面"},
			{"query": "b", "label": "负面"},
			{"query": "c", "label": "中性"},
		},
	})
	if !env.OK {
		t.Fatalf("update failed: %+v", env.Err)
	}
	_, env = h.do(t, http.MethodGet, "/v1/datasets/"+dsID, nil)
	dataInto(t, env, &getResp)
	if getResp.RowCount != 3 || getResp.Name != "情感评测集v2" {
		t.Fatalf("after update: name=%q row_count=%d, want 情感评测集v2/3", getResp.Name, getResp.RowCount)
	}

	// 非数组 rows → 400。
	rec, env := h.do(t, http.MethodPost, "/v1/datasets", map[string]any{
		"project_id": projID,
		"name":       "坏评测集",
		"rows":       map[string]any{"not": "array"},
	})
	if env.OK || rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for non-array rows, got code=%d env=%+v", rec.Code, env)
	}

	// Delete（软删）→ 之后 Get 应 404。
	_, env = h.do(t, http.MethodDelete, "/v1/datasets/"+dsID, nil)
	if !env.OK {
		t.Fatalf("delete failed: %+v", env.Err)
	}
	rec, env = h.do(t, http.MethodGet, "/v1/datasets/"+dsID, nil)
	if env.OK || rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404 after soft-delete, got code=%d env=%+v", rec.Code, env)
	}
}

func containsDS(items []struct {
	DatasetID string `json:"dataset_id"`
}, id string) bool {
	for _, it := range items {
		if it.DatasetID == id {
			return true
		}
	}
	return false
}
