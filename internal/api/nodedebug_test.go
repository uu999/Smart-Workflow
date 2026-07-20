package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/smart-workflow/smart-workflow/internal/dsl"
	"github.com/smart-workflow/smart-workflow/internal/engine"
	"github.com/smart-workflow/smart-workflow/internal/httpx"
	"github.com/smart-workflow/smart-workflow/internal/service"
	"github.com/smart-workflow/smart-workflow/internal/validator"
)

// TestFailFromErr_ValidationError 守卫 TD-10 gate 的错误映射：
// *service.ValidationError → 422 VALIDATION_FAILED，且 details.issues 带上问题清单。
func TestFailFromErr_ValidationError(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)

	verr := &service.ValidationError{Issues: []validator.Issue{
		{Code: "start_count", Severity: validator.SeverityError, Message: "no start"},
	}}
	failFromErr(c, verr)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422", rec.Code)
	}
	var env httpx.Envelope
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("unmarshal envelope: %v", err)
	}
	if env.OK || env.Error == nil || env.Error.Code != "VALIDATION_FAILED" {
		t.Fatalf("unexpected envelope: %+v", env)
	}
	// details 应携带 issues（Agent 据此自动修复）。
	details, ok := env.Error.Details.(map[string]any)
	if !ok || details["issues"] == nil {
		t.Fatalf("expected details.issues, got %+v", env.Error.Details)
	}
}

// TestNodeDebug_Stateless 验证无状态 node-debug 端点（风险1）：
// 直接吃渲染后的 code DSL 节点 + inputs，复用 engine.DebugNode 打 mock sidecar，
// 返回 DebugResult（status succeeded + 断言）。全程不碰存储。
func TestNodeDebug_Stateless(t *testing.T) {
	gin.SetMode(gin.TestMode)

	// mock sidecar：实现 code 节点契约，把 inputs.n 翻倍。
	sidecar := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/run/python-code" {
			var req struct {
				Inputs map[string]any `json:"inputs"`
			}
			_ = json.NewDecoder(r.Body).Decode(&req)
			n, _ := req.Inputs["n"].(float64)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"ok":   true,
				"data": map[string]any{"outputs": map[string]any{"doubled": n * 2}, "logs": ""},
			})
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer sidecar.Close()

	// engine.New 的 store 传 nil：DebugNode 只用 registry，不触库。
	h := &handlers{engine: engine.New(nil, sidecar.URL)}

	body, _ := json.Marshal(statelessNodeDebugReq{
		Node: dsl.DSLNode{
			ID: "code::1",
			Data: dsl.NodeData{
				NodeMeta:    dsl.NodeMeta{NodeType: dsl.KindCode, AliasName: "code"},
				NodeParam:   map[string]any{"code": "sink({'doubled': inputs['n']*2})"},
				RetryConfig: dsl.DefaultRetryConfig(),
			},
		},
		Inputs:        map[string]any{"n": 21.0},
		CostTargetSec: 5,
	})

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/node-debug", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	h.debugNode(c)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var env struct {
		OK   bool                `json:"ok"`
		Data engine.DebugResult `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("unmarshal: %v; body=%s", err, rec.Body.String())
	}
	if !env.OK {
		t.Fatalf("ok=false: %s", rec.Body.String())
	}
	if env.Data.Status != "succeeded" {
		t.Fatalf("status = %q, want succeeded", env.Data.Status)
	}
	if env.Data.Output["doubled"] != 42.0 {
		t.Fatalf("doubled = %v, want 42", env.Data.Output["doubled"])
	}
	// cost_target_sec>0 → 应有 3 条断言。
	if len(env.Data.Assertions) != 3 {
		t.Fatalf("assertions = %d, want 3: %+v", len(env.Data.Assertions), env.Data.Assertions)
	}
}

// TestNodeDebug_MissingFields 守卫入参校验：缺 node.id / nodeType → 400。
func TestNodeDebug_MissingFields(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := &handlers{engine: engine.New(nil, "")}

	cases := []struct {
		name string
		body statelessNodeDebugReq
	}{
		{"missing id", statelessNodeDebugReq{Node: dsl.DSLNode{Data: dsl.NodeData{NodeMeta: dsl.NodeMeta{NodeType: "code"}}}}},
		{"missing type", statelessNodeDebugReq{Node: dsl.DSLNode{ID: "code::1"}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body, _ := json.Marshal(tc.body)
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/node-debug", bytes.NewReader(body))
			c.Request.Header.Set("Content-Type", "application/json")

			h.debugNode(c)

			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400", rec.Code)
			}
		})
	}
}
