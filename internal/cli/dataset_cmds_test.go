package cli

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// datasetServer 起假服务端：POST /v1/datasets 回 dataset_id；GET /v1/datasets 回 items；
// GET /v1/datasets/{id} 回详情。捕获收到的请求体供断言。
func datasetServer(t *testing.T, captured *map[string]any) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/datasets":
			body, _ := io.ReadAll(r.Body)
			if captured != nil {
				_ = json.Unmarshal(body, captured)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"ok":   true,
				"data": map[string]any{"dataset_id": "ds_new"},
			})
		case r.Method == http.MethodGet && r.URL.Path == "/v1/datasets":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"ok": true,
				"data": map[string]any{"items": []any{
					map[string]any{"dataset_id": "ds_a", "name": "情感评测集", "row_count": 2},
				}},
			})
		case r.Method == http.MethodGet && len(r.URL.Path) > len("/v1/datasets/"):
			_ = json.NewEncoder(w).Encode(map[string]any{
				"ok": true,
				"data": map[string]any{
					"dataset_id": "ds_a", "name": "情感评测集", "row_count": 2,
					"rows": []any{map[string]any{"query": "好评", "label": "正面"}},
				},
			})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
}

// writeRows 在临时目录写一个 rows JSON 文件，返回路径。
func writeRows(t *testing.T, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "rows.json")
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// TestCLI_DatasetCreate_FromFile 验证 dataset-create 读 JSON 数组文件→POST，
// 请求体带 project_id/name/rows，返回 dataset_id 与本地派生 row_count。
func TestCLI_DatasetCreate_FromFile(t *testing.T) {
	var got map[string]any
	srv := datasetServer(t, &got)
	defer srv.Close()

	rowsPath := writeRows(t, `[{"query":"这家店真棒","label":"正面"},{"query":"太差了","label":"负面"}]`)
	env, err := runCLI(t, "dataset-create", "--server", srv.URL,
		"--project-id", "6970", "--name", "情感评测集", "--file", rowsPath)
	if err != nil || !env.OK {
		t.Fatalf("dataset-create failed: err=%v env=%+v", err, env)
	}
	if id, _ := dataField(t, env, "dataset_id").(string); id != "ds_new" {
		t.Fatalf("dataset_id = %v, want ds_new", dataField(t, env, "dataset_id"))
	}
	if n, _ := dataField(t, env, "row_count").(float64); n != 2 {
		t.Fatalf("row_count = %v, want 2", dataField(t, env, "row_count"))
	}
	// 服务端应收到 project_id/name，且 rows 是数组。
	if got["project_id"] != "6970" || got["name"] != "情感评测集" {
		t.Fatalf("server got wrong fields: %+v", got)
	}
	if arr, ok := got["rows"].([]any); !ok || len(arr) != 2 {
		t.Fatalf("server rows not a 2-elem array: %+v", got["rows"])
	}
}

// TestCLI_DatasetCreate_NotArray 验证非数组文件本地早失败 → INVALID_JSON。
func TestCLI_DatasetCreate_NotArray(t *testing.T) {
	rowsPath := writeRows(t, `{"query":"不是数组"}`)
	env, err := runCLI(t, "dataset-create", "--server", "http://127.0.0.1:1",
		"--project-id", "p1", "--name", "x", "--file", rowsPath)
	if err == nil {
		t.Fatal("expected error for non-array rows")
	}
	if env.OK || env.Error == nil || env.Error.Code != "INVALID_JSON" {
		t.Fatalf("expected INVALID_JSON, got %+v", env)
	}
}

// TestCLI_DatasetCreate_MissingFlags 验证缺 --file → BAD_REQUEST。
func TestCLI_DatasetCreate_MissingFlags(t *testing.T) {
	env, err := runCLI(t, "dataset-create", "--server", "http://127.0.0.1:1",
		"--project-id", "p1", "--name", "x")
	if err == nil {
		t.Fatal("expected error for missing --file")
	}
	if env.OK || env.Error == nil || env.Error.Code != "BAD_REQUEST" {
		t.Fatalf("expected BAD_REQUEST, got %+v", env)
	}
}

// TestCLI_DatasetList 验证 dataset-list 透传 items。
func TestCLI_DatasetList(t *testing.T) {
	srv := datasetServer(t, nil)
	defer srv.Close()

	env, err := runCLI(t, "dataset-list", "--server", srv.URL, "--project-id", "6970")
	if err != nil || !env.OK {
		t.Fatalf("dataset-list failed: err=%v env=%+v", err, env)
	}
	items, ok := dataField(t, env, "items").([]any)
	if !ok || len(items) != 1 {
		t.Fatalf("items = %v, want 1 elem", dataField(t, env, "items"))
	}
}

// TestCLI_DatasetGet 验证 dataset-get 透传详情（含 rows）。
func TestCLI_DatasetGet(t *testing.T) {
	srv := datasetServer(t, nil)
	defer srv.Close()

	env, err := runCLI(t, "dataset-get", "--server", srv.URL, "--id", "ds_a")
	if err != nil || !env.OK {
		t.Fatalf("dataset-get failed: err=%v env=%+v", err, env)
	}
	m, ok := env.Data.(map[string]any)
	if !ok || m["dataset_id"] != "ds_a" {
		t.Fatalf("data = %+v, want dataset_id=ds_a", env.Data)
	}
}

// TestCLI_DatasetGet_MissingID 验证缺 --id → BAD_REQUEST。
func TestCLI_DatasetGet_MissingID(t *testing.T) {
	env, err := runCLI(t, "dataset-get", "--server", "http://127.0.0.1:1")
	if err == nil {
		t.Fatal("expected error for missing --id")
	}
	if env.OK || env.Error == nil || env.Error.Code != "BAD_REQUEST" {
		t.Fatalf("expected BAD_REQUEST, got %+v", env)
	}
}
