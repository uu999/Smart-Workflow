package engine

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/smart-workflow/smart-workflow/internal/dsl"
)

// DebugNode 不触碰 store，故可用 New(nil, url) 构造引擎单测。
func TestDebugNode_HTTPSuccessAssertions(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"message": "pong"})
	}))
	defer srv.Close()

	e := New(nil, "")
	node := dsl.DSLNode{
		ID: "http::1",
		Data: dsl.NodeData{
			NodeMeta:    dsl.NodeMeta{NodeType: "http"},
			NodeParam:   map[string]any{"method": "GET", "url": srv.URL + "/echo"},
			RetryConfig: dsl.DefaultRetryConfig(),
		},
	}

	res := e.DebugNode(context.Background(), node, map[string]any{}, 5)
	if res.Status != "succeeded" {
		t.Fatalf("status = %s, want succeeded; err=%s", res.Status, res.Error)
	}
	if res.Output["status_code"] != 200 {
		t.Fatalf("status_code = %v, want 200", res.Output["status_code"])
	}
	// 三条断言：status_success / output_not_empty / cost_under_sec(target=5)
	want := map[string]bool{"status_success": true, "output_not_empty": true, "cost_under_sec": true}
	if len(res.Assertions) != 3 {
		t.Fatalf("assertions count = %d, want 3: %+v", len(res.Assertions), res.Assertions)
	}
	for _, a := range res.Assertions {
		if !a.Pass {
			t.Fatalf("assertion %s should pass", a.Type)
		}
		delete(want, a.Type)
	}
	if len(want) != 0 {
		t.Fatalf("missing assertions: %v", want)
	}
}

func TestDebugNode_NoCostTargetOmitsAssertion(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"ok":1}`))
	}))
	defer srv.Close()

	e := New(nil, "")
	node := dsl.DSLNode{
		ID: "http::1",
		Data: dsl.NodeData{
			NodeMeta:    dsl.NodeMeta{NodeType: "http"},
			NodeParam:   map[string]any{"url": srv.URL},
			RetryConfig: dsl.DefaultRetryConfig(),
		},
	}
	res := e.DebugNode(context.Background(), node, map[string]any{}, 0)
	// costTarget=0 时不追加 cost_under_sec，只有 2 条。
	if len(res.Assertions) != 2 {
		t.Fatalf("assertions count = %d, want 2", len(res.Assertions))
	}
	for _, a := range res.Assertions {
		if a.Type == "cost_under_sec" {
			t.Fatal("cost_under_sec should be omitted when target=0")
		}
	}
}

func TestDebugNode_UnknownTypeFails(t *testing.T) {
	e := New(nil, "")
	node := dsl.DSLNode{
		ID:   "mystery::1",
		Data: dsl.NodeData{NodeMeta: dsl.NodeMeta{NodeType: "mystery"}},
	}
	res := e.DebugNode(context.Background(), node, map[string]any{}, 0)
	if res.Status != "failed" {
		t.Fatalf("status = %s, want failed", res.Status)
	}
	if res.Error == "" {
		t.Fatal("expected error message for unknown node type")
	}
	// status_success 断言应为 false。
	for _, a := range res.Assertions {
		if a.Type == "status_success" && a.Pass {
			t.Fatal("status_success should be false for failed node")
		}
	}
}
