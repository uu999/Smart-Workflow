package cli

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestCLI_Search_Application 验证 search 转发到 /v1/applications 并带上 name/project_id。
func TestCLI_Search_Application(t *testing.T) {
	t.Setenv("SWF_SESSIONS_DIR", t.TempDir())

	var gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/applications" {
			gotQuery = r.URL.RawQuery
			_ = json.NewEncoder(w).Encode(map[string]any{
				"ok":   true,
				"data": map[string]any{"items": []any{map[string]any{"app_id": "app_1", "name": "qwen-cls"}}},
			})
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	env, err := runCLI(t, "search", "--server", srv.URL, "--kind", "application", "--name", "qwen", "--project-id", "6970")
	if err != nil || !env.OK {
		t.Fatalf("search failed: err=%v env=%+v", err, env)
	}
	if !contains(gotQuery, "project_id=6970") || !contains(gotQuery, "name=qwen") {
		t.Fatalf("query missing params: %q", gotQuery)
	}
}

// TestCLI_Search_DatasetNotSupported 验证 dataset kind 明确回 NOT_SUPPORTED（M8 决策）。
func TestCLI_Search_DatasetNotSupported(t *testing.T) {
	env, err := runCLI(t, "search", "--kind", "dataset", "--project-id", "6970")
	if err == nil {
		t.Fatal("dataset search should error in M8")
	}
	if env.OK || env.Error == nil || env.Error.Code != "NOT_SUPPORTED" {
		t.Fatalf("expected NOT_SUPPORTED, got %+v", env)
	}
}

// TestCLI_Search_RequiresProject 验证缺 project-id 报 BAD_REQUEST。
func TestCLI_Search_RequiresProject(t *testing.T) {
	env, err := runCLI(t, "search", "--kind", "application")
	if err == nil || env.Error == nil || env.Error.Code != "BAD_REQUEST" {
		t.Fatalf("expected BAD_REQUEST, got env=%+v err=%v", env, err)
	}
}

// TestCLI_AppSchema_MaterializesPorts 验证 app-schema 把端口物化进已有 app 节点的
// IR（决策Q1），并缓存原文到 app_cache。
func TestCLI_AppSchema_MaterializesPorts(t *testing.T) {
	t.Setenv("SWF_SESSIONS_DIR", t.TempDir())

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/applications/app_1" {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"ok": true,
				"data": map[string]any{
					"app_id":        "app_1",
					"name":          "情感分类器",
					"input_schema":  []any{map[string]any{"name": "text", "type": "string", "required": true}},
					"output_schema": []any{map[string]any{"name": "label", "type": "string"}, map[string]any{"name": "confidence", "type": "number"}},
				},
			})
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	env, _ := runCLI(t, "init", "--name", "sch")
	sid := dataField(t, env, "sid").(string)
	// 先加一个引用 app_1 的 application 节点（尚无端口）。
	if _, err := runCLI(t, "add-node", "--sid", sid, "--id", "app_1", "--kind", "application", "--app-id", "app_1"); err != nil {
		t.Fatalf("add-node: %v", err)
	}

	env, err := runCLI(t, "app-schema", "--server", srv.URL, "--sid", sid, "--app-id", "app_1")
	if err != nil || !env.OK {
		t.Fatalf("app-schema failed: err=%v env=%+v", err, env)
	}
	if m, _ := dataField(t, env, "materialized").(bool); !m {
		t.Fatal("expected materialized=true (node app_1 exists)")
	}

	// 端口应已落进 ir.json 的节点声明。
	s, lerr := LoadSession(sid)
	if lerr != nil {
		t.Fatalf("load: %v", lerr)
	}
	node := s.IR.FindNode("app_1")
	if node == nil || len(node.Inputs) != 1 || len(node.Outputs) != 2 {
		t.Fatalf("ports not materialized into IR: %+v", node)
	}
	if node.Inputs[0].Name != "text" || !node.Inputs[0].Required {
		t.Errorf("input port lost: %+v", node.Inputs)
	}
}

// TestCLI_Scope_ConnectivityAndType 验证 scope 只返回传递上游、且类型兼容的候选。
func TestCLI_Scope_ConnectivityAndType(t *testing.T) {
	t.Setenv("SWF_SESSIONS_DIR", t.TempDir())

	env, _ := runCLI(t, "init", "--name", "scope")
	sid := dataField(t, env, "sid").(string)

	// start_0(query:string, n:number) -> app_1(text:string) ; 另有孤立 iso(x:string) 不连通。
	_, _ = runCLI(t, "add-node", "--sid", sid, "--id", "start_0", "--kind", "start", "--outputs", "query:string,n:number")
	_, _ = runCLI(t, "add-node", "--sid", sid, "--id", "app_1", "--kind", "application", "--app-id", "a", "--inputs", "text:string")
	_, _ = runCLI(t, "add-node", "--sid", sid, "--id", "iso", "--kind", "application", "--app-id", "b", "--outputs", "x:string")
	_, _ = runCLI(t, "add-edge", "--sid", sid, "--source", "start_0", "--target", "app_1")

	env, err := runCLI(t, "scope", "--sid", sid, "--node-id", "app_1", "--port", "text")
	if err != nil || !env.OK {
		t.Fatalf("scope failed: err=%v env=%+v", err, env)
	}
	m := env.Data.(map[string]any)
	if m["want_type"] != "string" {
		t.Fatalf("want_type = %v, want string", m["want_type"])
	}
	cands, _ := m["candidates"].([]any)
	// 只应有 start_0.query（string，连通）；start_0.n 类型不符被过滤；iso.x 不连通被过滤。
	if len(cands) != 1 {
		t.Fatalf("candidates = %d, want 1: %+v", len(cands), cands)
	}
	c0 := cands[0].(map[string]any)
	if c0["source_node"] != "start_0" || c0["source_port"] != "query" {
		t.Fatalf("candidate = %+v, want start_0.query", c0)
	}
}

// TestScopeCandidates_UnknownTypeNoFilter 单测：目标端口类型未知时不做类型过滤。
func TestScopeCandidates_UnknownTypeNoFilter(t *testing.T) {
	t.Setenv("SWF_SESSIONS_DIR", t.TempDir())
	env, _ := runCLI(t, "init", "--name", "u")
	sid := dataField(t, env, "sid").(string)
	_, _ = runCLI(t, "add-node", "--sid", sid, "--id", "start_0", "--kind", "start", "--outputs", "a:string,b:number")
	// 目标端口 p 未声明类型。
	_, _ = runCLI(t, "add-node", "--sid", sid, "--id", "app_1", "--kind", "application", "--app-id", "x")
	_, _ = runCLI(t, "add-edge", "--sid", sid, "--source", "start_0", "--target", "app_1")

	env, err := runCLI(t, "scope", "--sid", sid, "--node-id", "app_1", "--port", "p")
	if err != nil || !env.OK {
		t.Fatalf("scope failed: %+v", env)
	}
	m := env.Data.(map[string]any)
	cands, _ := m["candidates"].([]any)
	// 类型未知：start_0 的两个输出都应作为候选。
	if len(cands) != 2 {
		t.Fatalf("candidates = %d, want 2 (no type filter): %+v", len(cands), cands)
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || indexOf(s, sub) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
