package cli

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// cloneServer 起一个假服务端：GET /v1/workflows/{id} 返回带 draft DSL 的工作流。
func cloneServer(t *testing.T, draft map[string]any) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && len(r.URL.Path) > len("/v1/workflows/") {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"ok": true,
				"data": map[string]any{
					"workflow_id": "wf_src",
					"name":        "情感评测源图",
					"project_id":  "6970",
					"draft":       draft,
				},
			})
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
}

// startEndDraft 构造一个最小可反渲染的 DSL（start→end），供 clone-ref 拉取。
func startEndDraft() map[string]any {
	return map[string]any{
		"nodes": []any{
			map[string]any{
				"id":   "start::a",
				"data": map[string]any{"nodeMeta": map[string]any{"nodeType": "start", "aliasName": "开始"}},
			},
			map[string]any{
				"id":   "end::b",
				"data": map[string]any{"nodeMeta": map[string]any{"nodeType": "end", "aliasName": "结束"}},
			},
		},
		"edges": []any{
			map[string]any{"sourceNodeID": "start::a", "targetNodeID": "end::b"},
		},
	}
}

// TestCLI_CloneRef_CreatesSession 验证 clone-ref 拉服务端 DSL→反渲染成本地 IR 会话，
// 回填 Source，节点数正确。
func TestCLI_CloneRef_CreatesSession(t *testing.T) {
	t.Setenv("SWF_SESSIONS_DIR", t.TempDir())
	srv := cloneServer(t, startEndDraft())
	defer srv.Close()

	env, err := runCLI(t, "clone-ref", "--server", srv.URL, "--workflow-id", "wf_src")
	if err != nil || !env.OK {
		t.Fatalf("clone-ref failed: err=%v env=%+v", err, env)
	}
	if src, _ := dataField(t, env, "source").(string); src != "wf_src" {
		t.Fatalf("source = %v, want wf_src", dataField(t, env, "source"))
	}
	// start+end → 2 节点。
	if n, _ := dataField(t, env, "node_num").(float64); n != 2 {
		t.Fatalf("node_num = %v, want 2", dataField(t, env, "node_num"))
	}
	// 缺省沿用来源工作流名。
	if name, _ := dataField(t, env, "name").(string); name != "情感评测源图" {
		t.Fatalf("name = %v, want 情感评测源图", dataField(t, env, "name"))
	}

	// 会话应可被后续命令加载（validate 能读到）。
	sid := dataField(t, env, "sid").(string)
	if _, verr := runCLI(t, "preview", "--sid", sid); verr != nil {
		t.Fatalf("cloned session not loadable by preview: %v", verr)
	}
}

// TestCLI_CloneRef_MissingWorkflowID 验证缺 --workflow-id → BAD_REQUEST。
func TestCLI_CloneRef_MissingWorkflowID(t *testing.T) {
	env, err := runCLI(t, "clone-ref", "--server", "http://127.0.0.1:1")
	if err == nil {
		t.Fatal("expected error for missing workflow-id")
	}
	if env.OK || env.Error == nil || env.Error.Code != "BAD_REQUEST" {
		t.Fatalf("expected BAD_REQUEST, got %+v", env)
	}
}

// TestCLI_CloneRef_EmptyWorkflow 验证来源工作流无图 → EMPTY_WORKFLOW。
func TestCLI_CloneRef_EmptyWorkflow(t *testing.T) {
	t.Setenv("SWF_SESSIONS_DIR", t.TempDir())
	srv := cloneServer(t, map[string]any{"nodes": []any{}, "edges": []any{}})
	defer srv.Close()

	env, err := runCLI(t, "clone-ref", "--server", srv.URL, "--workflow-id", "wf_empty")
	if err == nil {
		t.Fatal("expected error for empty workflow")
	}
	if env.OK || env.Error == nil || env.Error.Code != "EMPTY_WORKFLOW" {
		t.Fatalf("expected EMPTY_WORKFLOW, got %+v", env)
	}
}

// TestCLI_CloneRef_ServerNotFound 验证服务端 404 透传为结构化错误。
func TestCLI_CloneRef_ServerNotFound(t *testing.T) {
	t.Setenv("SWF_SESSIONS_DIR", t.TempDir())
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"ok":false,"error":{"code":"NOT_FOUND","message":"workflow not found"}}`))
	}))
	defer srv.Close()

	env, err := runCLI(t, "clone-ref", "--server", srv.URL, "--workflow-id", "ghost")
	if err == nil {
		t.Fatal("expected error for missing workflow")
	}
	if env.OK || env.Error == nil || env.Error.Code != "NOT_FOUND" {
		t.Fatalf("expected NOT_FOUND, got %+v", env)
	}
}
