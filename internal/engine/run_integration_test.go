//go:build integration

package engine

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/smart-workflow/smart-workflow/internal/dsl"
	"github.com/smart-workflow/smart-workflow/internal/engine/nodes"
	"github.com/smart-workflow/smart-workflow/internal/storage/mysql"
	"github.com/smart-workflow/smart-workflow/internal/storage/mysql/gen"
)

func TestM4_EngineRunPersistsSnapshots(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"message": r.URL.Query().Get("q"),
		})
	}))
	defer srv.Close()

	st := testStore(t)
	defer st.Close()

	ctx := context.Background()
	suffix := strconv.FormatInt(time.Now().UnixNano(), 10)
	workflowID := "wf_m4_" + suffix
	runID := "run_m4_" + suffix

	d := engineTestDSL(srv.URL)
	rawDSL, err := json.Marshal(d)
	if err != nil {
		t.Fatalf("marshal dsl: %v", err)
	}
	rawInput, err := json.Marshal(map[string]any{"query": "hello"})
	if err != nil {
		t.Fatalf("marshal input: %v", err)
	}

	defer func() {
		_, _ = st.DB.ExecContext(ctx, "DELETE FROM node_run WHERE run_id = ?", runID)
		_, _ = st.DB.ExecContext(ctx, "DELETE FROM workflow_run WHERE run_id = ?", runID)
		_, _ = st.DB.ExecContext(ctx, "DELETE FROM workflow_version WHERE workflow_id = ?", workflowID)
	}()

	if _, err := st.Q.CreateWorkflowVersion(ctx, gen.CreateWorkflowVersionParams{
		WorkflowID: workflowID,
		Version:    1,
		Dsl:        rawDSL,
		ChangeLog:  "m4 engine integration",
	}); err != nil {
		t.Fatalf("create workflow version: %v", err)
	}
	if _, err := st.Q.CreateWorkflowRun(ctx, gen.CreateWorkflowRunParams{
		RunID:       runID,
		WorkflowID:  workflowID,
		Version:     1,
		Status:      RunStatusPending,
		TriggerType: "test",
		Input:       rawInput,
	}); err != nil {
		t.Fatalf("create workflow run: %v", err)
	}

	e := New(st, "")
	e.Concurrency = 2
	if err := e.Run(ctx, runID); err != nil {
		t.Fatalf("engine run: %v", err)
	}

	run, err := st.Q.GetWorkflowRun(ctx, runID)
	if err != nil {
		t.Fatalf("get run: %v", err)
	}
	if run.Status != RunStatusSucceeded {
		t.Fatalf("run status = %s, want %s", run.Status, RunStatusSucceeded)
	}
	var output map[string]any
	if err := json.Unmarshal(run.Output, &output); err != nil {
		t.Fatalf("unmarshal run output: %v", err)
	}
	if got := output["output"]; got != "hello" {
		t.Fatalf("run output = %v, want hello", got)
	}

	nodeRuns, err := st.Q.ListNodeRuns(ctx, runID)
	if err != nil {
		t.Fatalf("list node runs: %v", err)
	}
	if len(nodeRuns) != 3 {
		t.Fatalf("node_run count = %d, want 3", len(nodeRuns))
	}
	for _, n := range nodeRuns {
		if n.Status != nodes.StatusSucceeded {
			t.Fatalf("node %s status = %s, want succeeded", n.NodeID, n.Status)
		}
		if !n.StartedAt.Valid || !n.FinishedAt.Valid {
			t.Fatalf("node %s missing timestamps: %+v", n.NodeID, n)
		}
		if len(n.Output) == 0 || string(n.Output) == "null" {
			t.Fatalf("node %s missing output snapshot", n.NodeID)
		}
	}
}

func testStore(t *testing.T) *mysql.Store {
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

func engineTestDSL(baseURL string) *dsl.DSL {
	return &dsl.DSL{
		Nodes: []dsl.DSLNode{
			engineTestNode("start::1", dsl.KindStart, nil, nil),
			engineTestNode("http::1", "http", map[string]any{
				"method": "GET",
				"url":    baseURL + "/echo?q={{q}}",
			}, []dsl.InputItem{engineRefInput("q", "start::1", "query")}),
			engineTestNode("end::1", dsl.KindEnd, map[string]any{
				"template": "{{answer}}",
			}, []dsl.InputItem{engineRefInput("answer", "http::1", "json.message")}),
		},
		Edges: []dsl.DSLEdge{
			{SourceNodeID: "start::1", TargetNodeID: "http::1"},
			{SourceNodeID: "http::1", TargetNodeID: "end::1"},
		},
	}
}

func engineTestNode(id, typ string, param map[string]any, inputs []dsl.InputItem) dsl.DSLNode {
	if param == nil {
		param = map[string]any{}
	}
	return dsl.DSLNode{
		ID: id,
		Data: dsl.NodeData{
			NodeMeta:    dsl.NodeMeta{NodeType: typ, AliasName: typ},
			NodeParam:   param,
			Inputs:      inputs,
			RetryConfig: dsl.DefaultRetryConfig(),
		},
	}
}

func engineRefInput(name, nodeID, port string) dsl.InputItem {
	return dsl.InputItem{
		ID:   "in-" + name,
		Name: name,
		Schema: dsl.InputSchema{
			Value: dsl.Value{
				Type:    dsl.DSLValueRef,
				Content: dsl.RefContent{NodeID: nodeID, Name: port},
			},
		},
	}
}
