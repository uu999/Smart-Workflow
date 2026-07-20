package nodes

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/smart-workflow/smart-workflow/internal/dsl"
)

// mockSidecar 起一个假 sidecar，按预设 envelope 回应 /run/python-code。
func mockSidecar(t *testing.T, handler http.HandlerFunc) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/run/python-code", handler)
	return httptest.NewServer(mux)
}

func codeNode(code string) dsl.DSLNode {
	return dsl.DSLNode{
		ID: "code::x",
		Data: dsl.NodeData{
			NodeMeta:  dsl.NodeMeta{NodeType: dsl.KindCode},
			NodeParam: map[string]any{"code": code, "timeout": float64(5)},
		},
	}
}

func TestCodeExecutor_Success(t *testing.T) {
	var gotReq codeRunRequest
	srv := mockSidecar(t, func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&gotReq)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok":   true,
			"data": map[string]any{"outputs": map[string]any{"sum": 15.0}, "logs": "hi\n"},
		})
	})
	defer srv.Close()

	exec := CodeExecutor{SidecarURL: srv.URL}
	res, err := exec.Execute(context.Background(), &ExecContext{
		Node:   codeNode("sink({'sum': inputs['a']+inputs['b']})"),
		Inputs: map[string]any{"a": 10, "b": 5},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Outputs["sum"] != 15.0 {
		t.Fatalf("outputs sum = %v, want 15", res.Outputs["sum"])
	}
	// 请求体应带上 code 与 inputs。
	if gotReq.Code == "" || gotReq.Inputs["a"] != 10.0 {
		t.Fatalf("request not forwarded correctly: %+v", gotReq)
	}
}

func TestCodeExecutor_RuntimeError(t *testing.T) {
	srv := mockSidecar(t, func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok":    false,
			"error": map[string]any{"code": "RUNTIME_ERROR", "message": "ValueError: boom"},
		})
	})
	defer srv.Close()

	exec := CodeExecutor{SidecarURL: srv.URL}
	_, err := exec.Execute(context.Background(), &ExecContext{
		Node:   codeNode("raise ValueError('boom')"),
		Inputs: map[string]any{},
	})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "RUNTIME_ERROR") || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("error should carry sidecar code/message, got: %v", err)
	}
}

func TestCodeExecutor_MissingCode(t *testing.T) {
	exec := CodeExecutor{SidecarURL: "http://127.0.0.1:0"}
	_, err := exec.Execute(context.Background(), &ExecContext{
		Node:   dsl.DSLNode{ID: "code::y", Data: dsl.NodeData{NodeMeta: dsl.NodeMeta{NodeType: dsl.KindCode}, NodeParam: map[string]any{}}},
		Inputs: map[string]any{},
	})
	if err == nil || !strings.Contains(err.Error(), "missing code") {
		t.Fatalf("expected missing code error, got: %v", err)
	}
}

func TestCodeExecutor_SidecarUnreachable(t *testing.T) {
	// 指向一个不监听的地址，客户端应报连接错误。
	exec := CodeExecutor{
		SidecarURL: "http://127.0.0.1:0",
		Client:     &http.Client{Timeout: time.Second},
	}
	_, err := exec.Execute(context.Background(), &ExecContext{
		Node:   codeNode("sink({})"),
		Inputs: map[string]any{},
	})
	if err == nil || !strings.Contains(err.Error(), "call sidecar") {
		t.Fatalf("expected call sidecar error, got: %v", err)
	}
}

func TestCodeExecutor_BadResponse(t *testing.T) {
	srv := mockSidecar(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("not json"))
	})
	defer srv.Close()

	exec := CodeExecutor{SidecarURL: srv.URL}
	_, err := exec.Execute(context.Background(), &ExecContext{
		Node:   codeNode("sink({})"),
		Inputs: map[string]any{},
	})
	if err == nil || !strings.Contains(err.Error(), "bad sidecar response") {
		t.Fatalf("expected bad response error, got: %v", err)
	}
}

func TestCodeExecutor_Registered(t *testing.T) {
	reg := NewDefaultRegistry(Config{SidecarURL: "http://example.invalid"})
	e, ok := reg.Get(dsl.KindCode)
	if !ok {
		t.Fatal("code executor not registered")
	}
	if e.Type() != "code" {
		t.Fatalf("type = %q, want code", e.Type())
	}
	// 配置应真正注入到执行器，而非被忽略（改造②回归守卫）。
	ce, ok := e.(CodeExecutor)
	if !ok {
		t.Fatalf("registered code executor has unexpected type %T", e)
	}
	if ce.SidecarURL != "http://example.invalid" {
		t.Fatalf("SidecarURL = %q, want injected config value", ce.SidecarURL)
	}
}
