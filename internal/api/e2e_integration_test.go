//go:build integration

package api

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/smart-workflow/smart-workflow/internal/config"
	"github.com/smart-workflow/smart-workflow/internal/dsl"
	"github.com/smart-workflow/smart-workflow/internal/engine"
	"github.com/smart-workflow/smart-workflow/internal/storage/mysql"
	"github.com/smart-workflow/smart-workflow/internal/storage/mysql/gen"
)

// 运行方式（需先 make infra-up + migrate-up）：
//
//	SWF_TEST_DSN='swf:swfpass@tcp(127.0.0.1:3308)/smart_workflow?parseTime=true' \
//	go test -tags integration ./internal/api/ -run TestM6_E2E -v
//
// 本测试清 TD-7 验收：装真实 store + engine + mock HTTP sidecar，
// 通过 HTTP 走 create→validate→publish→run→轮询 succeeded→node-debug 全链路，
// 断言统一 envelope 与 node_run 落库，并守卫 404/400 错误码映射。

// apiEnvelope 是测试侧的响应解析体（Data 用 RawMessage 便于按需二次解码）。
type apiEnvelope struct {
	OK   bool            `json:"ok"`
	Data json.RawMessage `json:"data"`
	Err  *struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

type e2eHarness struct {
	router http.Handler
	store  *mysql.Store
}

func (h *e2eHarness) do(t *testing.T, method, path string, body any) (*httptest.ResponseRecorder, apiEnvelope) {
	t.Helper()
	var reader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal body: %v", err)
		}
		reader = bytes.NewReader(b)
	}
	req := httptest.NewRequest(method, path, reader)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	rec := httptest.NewRecorder()
	h.router.ServeHTTP(rec, req)

	var env apiEnvelope
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("%s %s: unmarshal envelope: %v; body=%s", method, path, err, rec.Body.String())
	}
	return rec, env
}

// dataInto 把成功响应的 data 解到 target。
func dataInto(t *testing.T, env apiEnvelope, target any) {
	t.Helper()
	if !env.OK {
		t.Fatalf("expected ok=true, got error: %+v", env.Err)
	}
	if err := json.Unmarshal(env.Data, target); err != nil {
		t.Fatalf("unmarshal data: %v; data=%s", err, env.Data)
	}
}

func TestM6_E2E_FullChain(t *testing.T) {
	// mock sidecar：只实现 code 节点契约（对齐 nodes.codeRunResponse）。
	mockSidecar := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/run/python-code" {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"ok": true,
				"data": map[string]any{
					"outputs": map[string]any{"answer": "hello"},
					"logs":    "",
				},
			})
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer mockSidecar.Close()

	st := e2eTestStore(t)
	defer st.Close()

	eng := engine.New(st, mockSidecar.URL)
	router := NewRouter(Deps{
		Cfg:    &config.Config{Env: "dev", Sidecar: config.SidecarConfig{BaseURL: mockSidecar.URL}},
		Logger: zap.NewNop(),
		Store:  st,
		Engine: eng,
	})
	h := &e2eHarness{router: router, store: st}
	ctx := context.Background()

	// 1) 建项目。
	_, env := h.do(t, http.MethodPost, "/v1/projects", map[string]any{"name": "e2e-proj"})
	var projResp struct {
		ProjectID string `json:"project_id"`
	}
	dataInto(t, env, &projResp)
	if projResp.ProjectID == "" {
		t.Fatal("empty project_id")
	}

	// 2) 建工作流（start→code→end）。
	_, env = h.do(t, http.MethodPost, "/v1/workflows", map[string]any{
		"project_id":  projResp.ProjectID,
		"name":        "e2e-wf",
		"description": "m6 e2e",
		"draft":       e2eDSL(),
	})
	var wfResp struct {
		WorkflowID string `json:"workflow_id"`
	}
	dataInto(t, env, &wfResp)
	wfID := wfResp.WorkflowID
	if wfID == "" {
		t.Fatal("empty workflow_id")
	}

	// 统一清理（顺序：node_run→workflow_run→version→workflow→project）。
	defer func() {
		_, _ = st.DB.ExecContext(ctx, "DELETE nr FROM node_run nr JOIN workflow_run wr ON nr.run_id = wr.run_id WHERE wr.workflow_id = ?", wfID)
		_, _ = st.DB.ExecContext(ctx, "DELETE FROM workflow_run WHERE workflow_id = ?", wfID)
		_, _ = st.DB.ExecContext(ctx, "DELETE FROM workflow_version WHERE workflow_id = ?", wfID)
		_, _ = st.DB.ExecContext(ctx, "DELETE FROM workflow WHERE workflow_id = ?", wfID)
		_, _ = st.DB.ExecContext(ctx, "DELETE FROM project WHERE project_id = ?", projResp.ProjectID)
	}()

	// 3) 校验草稿：应无 error。
	_, env = h.do(t, http.MethodPost, "/v1/workflows/"+wfID+"/validate", nil)
	var valResp struct {
		HasError bool `json:"has_error"`
		Issues   []struct {
			Code     string `json:"code"`
			Severity string `json:"severity"`
		} `json:"issues"`
	}
	dataInto(t, env, &valResp)
	if valResp.HasError {
		t.Fatalf("validate should have no error, issues=%+v", valResp.Issues)
	}

	// 4) 发布：version=1。
	_, env = h.do(t, http.MethodPost, "/v1/workflows/"+wfID+"/publish", map[string]any{"change_log": "first"})
	var pubResp struct {
		Version int32 `json:"version"`
	}
	dataInto(t, env, &pubResp)
	if pubResp.Version != 1 {
		t.Fatalf("first version = %d, want 1", pubResp.Version)
	}

	// 5) 触发运行（省略 version → 取已发布版本），异步返回 pending。
	_, env = h.do(t, http.MethodPost, "/v1/runs", map[string]any{
		"workflow_id": wfID,
		"input":       map[string]any{"query": "hello"},
	})
	var runResp struct {
		RunID  string `json:"run_id"`
		Status string `json:"status"`
	}
	dataInto(t, env, &runResp)
	if runResp.RunID == "" || runResp.Status != "pending" {
		t.Fatalf("unexpected run response: %+v", runResp)
	}

	// 6) 轮询运行到终态（后台 goroutine 执行）。
	type nodeView struct {
		NodeID string `json:"node_id"`
		Status string `json:"status"`
	}
	var finalRun struct {
		Status string     `json:"status"`
		Nodes  []nodeView `json:"nodes"`
	}
	deadline := time.Now().Add(15 * time.Second)
	for {
		_, env = h.do(t, http.MethodGet, "/v1/runs/"+runResp.RunID, nil)
		var rv struct {
			Status string     `json:"status"`
			Nodes  []nodeView `json:"nodes"`
		}
		dataInto(t, env, &rv)
		if rv.Status == "succeeded" || rv.Status == "failed" {
			finalRun = rv
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("run did not reach terminal state; last status=%q", rv.Status)
		}
		time.Sleep(200 * time.Millisecond)
	}
	if finalRun.Status != "succeeded" {
		t.Fatalf("run status = %q, want succeeded", finalRun.Status)
	}
	// node_run 落库校验：3 个节点全 succeeded。
	if len(finalRun.Nodes) != 3 {
		t.Fatalf("node_run count = %d, want 3: %+v", len(finalRun.Nodes), finalRun.Nodes)
	}
	for _, n := range finalRun.Nodes {
		if n.Status != "succeeded" {
			t.Fatalf("node %s status = %q, want succeeded", n.NodeID, n.Status)
		}
	}

	// 7) node-debug（草稿里的 code 节点，关重试 + 3 断言）。
	_, env = h.do(t, http.MethodPost, "/v1/workflows/"+wfID+"/node-debug", map[string]any{
		"node_id":         "code::1",
		"inputs":          map[string]any{"query": "hi"},
		"cost_target_sec": 5,
	})
	var dbg struct {
		Status     string `json:"status"`
		Assertions []struct {
			Type string `json:"type"`
			Pass bool   `json:"pass"`
		} `json:"assertions"`
	}
	dataInto(t, env, &dbg)
	if dbg.Status != "succeeded" {
		t.Fatalf("node-debug status = %q, want succeeded", dbg.Status)
	}
	if len(dbg.Assertions) != 3 {
		t.Fatalf("node-debug assertions = %d, want 3: %+v", len(dbg.Assertions), dbg.Assertions)
	}
	var statusOK bool
	for _, a := range dbg.Assertions {
		if a.Type == "status_success" && a.Pass {
			statusOK = true
		}
	}
	if !statusOK {
		t.Fatalf("status_success assertion should pass: %+v", dbg.Assertions)
	}

	// 7b) TD-10 run gate：把草稿改成坏图（删掉 start，制造 start_count error），
	// 以 version=-1（草稿）触发 run，应被前置校验拦下 → 422 VALIDATION_FAILED + issues，
	// 且不产生新的 pending run（坏图不落库）。
	badDraft := e2eDSL()
	badDraft.Nodes = badDraft.Nodes[1:] // 去掉 start::1
	_, env = h.do(t, http.MethodPut, "/v1/workflows/"+wfID, map[string]any{
		"name":  "e2e-wf",
		"draft": badDraft,
	})
	if !env.OK {
		t.Fatalf("update draft to bad graph failed: %+v", env.Err)
	}
	draftVer := int32(-1)
	rec7b, env7b := h.do(t, http.MethodPost, "/v1/runs", map[string]any{
		"workflow_id": wfID,
		"version":     draftVer,
		"input":       map[string]any{"query": "hi"},
	})
	if rec7b.Code != http.StatusUnprocessableEntity {
		t.Fatalf("run on bad draft status = %d, want 422; body ok=%v", rec7b.Code, env7b.OK)
	}
	if env7b.OK || env7b.Err == nil || env7b.Err.Code != "VALIDATION_FAILED" {
		t.Fatalf("expected VALIDATION_FAILED envelope, got %+v", env7b)
	}

	// 8) 错误码守卫：不存在的 workflow → 404 NOT_FOUND.
	rec, env := h.do(t, http.MethodGet, "/v1/workflows/does-not-exist", nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("GET missing workflow status = %d, want 404", rec.Code)
	}
	if env.OK || env.Err == nil || env.Err.Code != "NOT_FOUND" {
		t.Fatalf("expected NOT_FOUND envelope, got %+v", env)
	}

	// 缺必填字段 → 400 BAD_REQUEST。
	rec, env = h.do(t, http.MethodPost, "/v1/workflows", map[string]any{})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("create workflow w/o fields status = %d, want 400", rec.Code)
	}
	if env.OK || env.Err == nil || env.Err.Code != "BAD_REQUEST" {
		t.Fatalf("expected BAD_REQUEST envelope, got %+v", env)
	}
}

// TestM6_E2E_FailRunTombstone 验证 FailRunWithError 给 pending run 打 failed 墓碑
// （硬伤1 兜底的核心保证：满载拒绝 / panic 时 run 不会永久卡 pending/running）。
func TestM6_E2E_FailRunTombstone(t *testing.T) {
	st := e2eTestStore(t)
	defer st.Close()
	eng := engine.New(st, "")
	ctx := context.Background()

	suffix := time.Now().UnixNano()
	runID := "run_tomb_" + itoa(suffix)
	wfID := "wf_tomb_" + itoa(suffix)

	if _, err := st.Q.CreateWorkflowRun(ctx, gen.CreateWorkflowRunParams{
		RunID:       runID,
		WorkflowID:  wfID,
		Version:     1,
		Status:      "pending",
		TriggerType: "test",
		Input:       json.RawMessage(`{}`),
	}); err != nil {
		t.Fatalf("create run: %v", err)
	}
	defer func() { _, _ = st.DB.ExecContext(ctx, "DELETE FROM workflow_run WHERE run_id = ?", runID) }()

	if err := eng.FailRunWithError(ctx, runID, "run rejected: dispatcher at capacity"); err != nil {
		t.Fatalf("FailRunWithError: %v", err)
	}

	run, err := st.Q.GetWorkflowRun(ctx, runID)
	if err != nil {
		t.Fatalf("get run: %v", err)
	}
	if run.Status != "failed" {
		t.Fatalf("status = %q, want failed", run.Status)
	}
	if !run.Error.Valid || run.Error.String == "" {
		t.Fatal("failed run should carry an error message")
	}
	if !run.FinishedAt.Valid {
		t.Fatal("failed run should have finished_at set")
	}
}

func e2eTestStore(t *testing.T) *mysql.Store {
	t.Helper()
	dsn := os.Getenv("SWF_TEST_DSN")
	if dsn == "" {
		dsn = "swf:swfpass@tcp(127.0.0.1:3308)/smart_workflow?parseTime=true"
	}
	st, err := mysql.Open(dsn)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	return st
}

// itoa 是本测试文件的轻量 int64→string（避免额外 import 漂移）。
func itoa(n int64) string {
	return strconv.FormatInt(n, 10)
}

// e2eDSL 构造 start→code→end 图；code 节点声明 answer 输出，供 end 绑定与校验通过。
func e2eDSL() *dsl.DSL {
	return &dsl.DSL{
		Nodes: []dsl.DSLNode{
			{
				ID: "start::1",
				Data: dsl.NodeData{
					NodeMeta:    dsl.NodeMeta{NodeType: dsl.KindStart, AliasName: "开始"},
					Outputs:     []dsl.OutputItem{{ID: "o-q", Name: "query", Schema: map[string]any{"type": "string"}}},
					RetryConfig: dsl.DefaultRetryConfig(),
				},
			},
			{
				ID: "code::1",
				Data: dsl.NodeData{
					NodeMeta:    dsl.NodeMeta{NodeType: dsl.KindCode, AliasName: "代码"},
					NodeParam:   map[string]any{"code": "sink({'answer': inputs['query']})"},
					Inputs:      []dsl.InputItem{e2eRefInput("query", "string", "start::1", "query")},
					Outputs:     []dsl.OutputItem{{ID: "o-a", Name: "answer", Schema: map[string]any{"type": "string"}}},
					RetryConfig: dsl.DefaultRetryConfig(),
				},
			},
			{
				ID: "end::1",
				Data: dsl.NodeData{
					NodeMeta:    dsl.NodeMeta{NodeType: dsl.KindEnd, AliasName: "结束"},
					Inputs:      []dsl.InputItem{e2eRefInput("answer", "string", "code::1", "answer")},
					RetryConfig: dsl.DefaultRetryConfig(),
				},
			},
		},
		Edges: []dsl.DSLEdge{
			{SourceNodeID: "start::1", TargetNodeID: "code::1"},
			{SourceNodeID: "code::1", TargetNodeID: "end::1"},
		},
	}
}

func e2eRefInput(name, typ, srcNode, srcPort string) dsl.InputItem {
	return dsl.InputItem{
		ID:   "in-" + name,
		Name: name,
		Schema: dsl.InputSchema{
			Type: typ,
			Value: dsl.Value{
				Type:    dsl.DSLValueRef,
				Content: dsl.RefContent{NodeID: srcNode, Name: srcPort},
			},
		},
	}
}
