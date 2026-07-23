package nodes

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/smart-workflow/smart-workflow/internal/dsl"
)

// mockAppResolver 是可注入的假应用解析器。
type mockAppResolver struct {
	info *AppInfo
	err  error
}

func (m mockAppResolver) ResolveApp(_ context.Context, _ string) (*AppInfo, error) {
	return m.info, m.err
}

func appNode(appID string) dsl.DSLNode {
	p := map[string]any{}
	if appID != "" {
		p["appId"] = appID
	}
	return dsl.DSLNode{
		ID:   "application::x",
		Data: dsl.NodeData{NodeMeta: dsl.NodeMeta{NodeType: dsl.KindApplication}, NodeParam: p},
	}
}

// TestApplicationExecutor_HTTPKind 验证 kind=http 委托给 HTTPExecutor：
// config 提供的 url/method 应生效，返回底层 http 输出。
func TestApplicationExecutor_HTTPKind(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"label": "正面", "confidence": 0.9})
	}))
	defer srv.Close()

	exec := ApplicationExecutor{
		Resolver: mockAppResolver{info: &AppInfo{AppID: "app_http", Kind: "http", Config: map[string]any{
			"url":    srv.URL,
			"method": "GET",
		}}},
		HTTP: HTTPExecutor{},
	}
	res, err := exec.Execute(context.Background(), &ExecContext{Node: appNode("app_http")})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// HTTPExecutor 把可解析 JSON body 放到 json 输出。
	j, ok := res.Outputs["json"].(map[string]any)
	if !ok || j["label"] != "正面" {
		t.Fatalf("http delegate output mismatch: %+v", res.Outputs)
	}
}

// TestApplicationExecutor_PythonKind 验证 kind=python 委托给 CodeExecutor（打 mock sidecar）。
func TestApplicationExecutor_PythonKind(t *testing.T) {
	srv := mockSidecar(t, func(w http.ResponseWriter, r *http.Request) {
		var req codeRunRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok":   true,
			"data": map[string]any{"outputs": map[string]any{"label": "负面"}, "logs": ""},
		})
	})
	defer srv.Close()

	exec := ApplicationExecutor{
		Resolver: mockAppResolver{info: &AppInfo{AppID: "app_py", Kind: "python", Config: map[string]any{
			"code": "sink({'label': 'x'})",
		}}},
		Code: CodeExecutor{SidecarURL: srv.URL},
	}
	res, err := exec.Execute(context.Background(), &ExecContext{Node: appNode("app_py")})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Outputs["label"] != "负面" {
		t.Fatalf("python delegate output mismatch: %+v", res.Outputs)
	}
}

// TestApplicationExecutor_RPCUnsupported 验证 kind=rpc 明确报「未支持」（无 RPC 传输层）。
func TestApplicationExecutor_RPCUnsupported(t *testing.T) {
	exec := ApplicationExecutor{Resolver: mockAppResolver{info: &AppInfo{AppID: "app_rpc", Kind: "rpc"}}}
	_, err := exec.Execute(context.Background(), &ExecContext{Node: appNode("app_rpc")})
	if err == nil || !strings.Contains(err.Error(), "rpc not supported") {
		t.Fatalf("expected rpc not supported error, got: %v", err)
	}
}

func TestApplicationExecutor_UnknownKind(t *testing.T) {
	exec := ApplicationExecutor{Resolver: mockAppResolver{info: &AppInfo{AppID: "a", Kind: "weird"}}}
	_, err := exec.Execute(context.Background(), &ExecContext{Node: appNode("a")})
	if err == nil || !strings.Contains(err.Error(), "unknown app kind") {
		t.Fatalf("expected unknown kind error, got: %v", err)
	}
}

func TestApplicationExecutor_MissingAppID(t *testing.T) {
	exec := ApplicationExecutor{Resolver: mockAppResolver{info: &AppInfo{Kind: "http"}}}
	_, err := exec.Execute(context.Background(), &ExecContext{Node: appNode("")})
	if err == nil || !strings.Contains(err.Error(), "missing app_id") {
		t.Fatalf("expected missing app_id error, got: %v", err)
	}
}

func TestApplicationExecutor_NilResolver(t *testing.T) {
	exec := ApplicationExecutor{}
	_, err := exec.Execute(context.Background(), &ExecContext{Node: appNode("a")})
	if err == nil || !strings.Contains(err.Error(), "no app resolver") {
		t.Fatalf("expected no resolver error, got: %v", err)
	}
}

// TestApplicationExecutor_ConfigOverridesNodeParam 验证应用 config 覆盖同名 nodeParam
// （config 是权威调用配置）。
func TestApplicationExecutor_ConfigOverridesNodeParam(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
	}))
	defer srv.Close()

	node := appNode("app_http")
	node.Data.NodeParam["url"] = "http://should-be-overridden.invalid/old" // 应被 config 覆盖

	exec := ApplicationExecutor{
		Resolver: mockAppResolver{info: &AppInfo{AppID: "app_http", Kind: "http", Config: map[string]any{
			"url":    srv.URL + "/new",
			"method": "GET",
		}}},
		HTTP: HTTPExecutor{},
	}
	if _, err := exec.Execute(context.Background(), &ExecContext{Node: node}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotPath != "/new" {
		t.Fatalf("config url should override nodeParam; hit path %q, want /new", gotPath)
	}
}
