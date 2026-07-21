package scheduler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/smart-workflow/smart-workflow/internal/dsl"
	"github.com/smart-workflow/smart-workflow/internal/engine/builder"
	"github.com/smart-workflow/smart-workflow/internal/engine/nodes"
	"github.com/smart-workflow/smart-workflow/internal/engine/varpool"
	"github.com/smart-workflow/smart-workflow/internal/runevent"
)

// captureEmitter 收集调度过程中发出的事件（单线程 loop 内调用，无需锁）。
type captureEmitter struct {
	events []runevent.RunEvent
}

func (c *captureEmitter) Emit(e runevent.RunEvent) { c.events = append(c.events, e) }

// TestRun_EmitsNodeEvents 验证调度发 node_start/node_end，且 seq 单调、
// node_start 先于对应 node_end（M9-b 事件产出）。
func TestRun_EmitsNodeEvents(t *testing.T) {
	d := &dsl.DSL{
		Nodes: []dsl.DSLNode{
			node("start::1", dsl.KindStart, nil, nil),
			node("end::1", dsl.KindEnd, map[string]any{"template": "{{q}}"},
				[]dsl.InputItem{refInput("q", "start::1", "query")}),
		},
		Edges: []dsl.DSLEdge{{SourceNodeID: "start::1", TargetNodeID: "end::1"}},
	}
	plan, err := builder.Build(d)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	cap := &captureEmitter{}
	_, err = Run(context.Background(), plan, varpool.New(), Options{
		RunID:       "run_evt",
		Input:       map[string]any{"query": "hi"},
		Concurrency: 4,
		Registry:    nodes.NewDefaultRegistry(nodes.Config{}),
		Emitter:     cap,
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	// seq 单调递增。
	for i := 1; i < len(cap.events); i++ {
		if cap.events[i].Seq <= cap.events[i-1].Seq {
			t.Fatalf("seq not monotonic at %d: %+v", i, cap.events)
		}
	}
	// 每个节点应有 node_start 与 node_end；start 在 end 之前。
	startSeq := map[string]int64{}
	endSeq := map[string]int64{}
	for _, e := range cap.events {
		switch e.Phase {
		case runevent.PhaseNodeStart:
			startSeq[e.NodeID] = e.Seq
		case runevent.PhaseNodeEnd:
			endSeq[e.NodeID] = e.Seq
		}
	}
	for _, id := range []string{"start::1", "end::1"} {
		s, okS := startSeq[id]
		en, okE := endSeq[id]
		if !okS || !okE {
			t.Fatalf("node %s missing start/end event: start=%v end=%v", id, okS, okE)
		}
		if s >= en {
			t.Errorf("node %s: start seq %d should precede end seq %d", id, s, en)
		}
	}
}

func TestRun_StartHTTPToEnd(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"message": r.URL.Query().Get("q"),
		})
	}))
	defer srv.Close()

	d := &dsl.DSL{
		Nodes: []dsl.DSLNode{
			node("start::1", dsl.KindStart, nil, nil),
			node("http::1", "http", map[string]any{
				"method": "GET",
				"url":    srv.URL + "/echo?q={{q}}",
			}, []dsl.InputItem{refInput("q", "start::1", "query")}),
			node("end::1", dsl.KindEnd, map[string]any{
				"template": "{{answer}}",
			}, []dsl.InputItem{refInput("answer", "http::1", "json.message")}),
		},
		Edges: []dsl.DSLEdge{
			{SourceNodeID: "start::1", TargetNodeID: "http::1"},
			{SourceNodeID: "http::1", TargetNodeID: "end::1"},
		},
	}

	result := runPlan(t, d, map[string]any{"query": "hello"})
	if got := result.Outputs["output"]; got != "hello" {
		t.Fatalf("end output = %v, want hello", got)
	}
	assertStatus(t, result, "start::1", nodes.StatusSucceeded)
	assertStatus(t, result, "http::1", nodes.StatusSucceeded)
	assertStatus(t, result, "end::1", nodes.StatusSucceeded)
}

func TestRun_ConditionBranchSkipsUnmatchedBranch(t *testing.T) {
	d := &dsl.DSL{
		Nodes: []dsl.DSLNode{
			node("start::1", dsl.KindStart, nil, nil),
			node("condition::1", dsl.KindCondition, map[string]any{
				"branches": []any{
					map[string]any{"index": 0, "conditions": []any{
						map[string]any{"left_port": "score", "comparator": "gte", "right": 0.8},
					}},
					map[string]any{"index": 1, "conditions": []any{
						map[string]any{"left_port": "score", "comparator": "lt", "right": 0.8},
					}},
				},
			}, []dsl.InputItem{refInput("score", "start::1", "score")}),
			node("end::pass", dsl.KindEnd, map[string]any{
				"template": "pass {{score}}",
			}, []dsl.InputItem{refInput("score", "start::1", "score")}),
			node("end::fail", dsl.KindEnd, map[string]any{
				"template": "fail {{score}}",
			}, []dsl.InputItem{refInput("score", "start::1", "score")}),
		},
		Edges: []dsl.DSLEdge{
			{SourceNodeID: "start::1", TargetNodeID: "condition::1"},
			{SourceNodeID: "condition::1", TargetNodeID: "end::pass", SourceHandle: "0"},
			{SourceNodeID: "condition::1", TargetNodeID: "end::fail", SourceHandle: "1"},
		},
	}

	result := runPlan(t, d, map[string]any{"score": 0.9})
	if got := result.Outputs["output"]; got != "pass 0.9" {
		t.Fatalf("end output = %v, want pass 0.9", got)
	}
	assertStatus(t, result, "condition::1", nodes.StatusSucceeded)
	assertStatus(t, result, "end::pass", nodes.StatusSucceeded)
	assertStatus(t, result, "end::fail", nodes.StatusSkipped)
}

// TestRun_RenderedConditionExecutes 是风险4的端到端回归防线：从 IR 经 Render
// 得到 DSL，再 Build+Run，验证渲染出的 condition 分支真能驱动剪枝（此前 render
// 丢弃 branches，rendered condition 执行期必报 "has no branches"）。
// 与上面手搓 nodeParam 的用例互补——那个测调度，这个测 render→execute 的接缝。
func TestRun_RenderedConditionExecutes(t *testing.T) {
	ir := &dsl.IR{
		Meta: dsl.Meta{Name: "cond"},
		Nodes: []dsl.Node{
			{ID: "start_0", Kind: dsl.KindStart, Outputs: []dsl.Port{{Name: "score", Type: dsl.ValueTypeNumber}}},
			{
				ID:   "cond_0",
				Kind: dsl.KindCondition,
				Bindings: []dsl.Binding{
					{Port: "score", Mode: dsl.BindModeRef, SourceNode: "start_0", SourcePort: "score"},
				},
				Branches: []dsl.Branch{
					{Index: 0, Conditions: []dsl.Condition{{LeftPort: "score", Comparator: "gte", Right: 0.8}}},
					{Index: 1, Conditions: []dsl.Condition{{LeftPort: "score", Comparator: "lt", Right: 0.8}}},
				},
			},
			{ID: "end_pass", Kind: dsl.KindEnd, Title: "pass", Params: map[string]any{"template": "pass"},
				Inputs:   []dsl.Port{{Name: "score", Type: dsl.ValueTypeNumber}},
				Bindings: []dsl.Binding{{Port: "score", Mode: dsl.BindModeRef, SourceNode: "start_0", SourcePort: "score"}}},
			{ID: "end_fail", Kind: dsl.KindEnd, Title: "fail", Params: map[string]any{"template": "fail"},
				Inputs:   []dsl.Port{{Name: "score", Type: dsl.ValueTypeNumber}},
				Bindings: []dsl.Binding{{Port: "score", Mode: dsl.BindModeRef, SourceNode: "start_0", SourcePort: "score"}}},
		},
		Edges: []dsl.Edge{
			{Source: "start_0", Target: "cond_0"},
			{Source: "cond_0", Target: "end_pass", SourcePort: "0"},
			{Source: "cond_0", Target: "end_fail", SourcePort: "1"},
		},
	}

	d, err := dsl.NewRenderer().Render(ir)
	if err != nil {
		t.Fatalf("render: %v", err)
	}

	// 从渲染出的 DSL 里取真实 ID（title 唯一标识两个 end 节点），供断言用。
	var condID, endPassID, endFailID string
	for _, n := range d.Nodes {
		switch n.Data.NodeMeta.NodeType {
		case dsl.KindCondition:
			condID = n.ID
		case dsl.KindEnd:
			switch n.Data.NodeMeta.AliasName {
			case "pass":
				endPassID = n.ID
			case "fail":
				endFailID = n.ID
			}
		}
	}
	if condID == "" || endPassID == "" || endFailID == "" {
		t.Fatalf("missing rendered ids: cond=%q pass=%q fail=%q", condID, endPassID, endFailID)
	}

	result := runPlan(t, d, map[string]any{"score": 0.9})
	assertStatus(t, result, condID, nodes.StatusSucceeded)
	assertStatus(t, result, endPassID, nodes.StatusSucceeded)
	assertStatus(t, result, endFailID, nodes.StatusSkipped)
}

func TestRun_StartCodeToEnd(t *testing.T) {
	// mock sidecar：把 inputs.n 翻倍后 sink 回 doubled。
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Inputs map[string]any `json:"inputs"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		n, _ := req.Inputs["n"].(float64)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok":   true,
			"data": map[string]any{"outputs": map[string]any{"doubled": n * 2}, "logs": ""},
		})
	}))
	defer srv.Close()

	// 用注入 SidecarURL 的 code 执行器构造注册表（避免打真实 sidecar，也不碰全局状态）。
	reg := nodes.NewDefaultRegistry(nodes.Config{SidecarURL: srv.URL})

	d := &dsl.DSL{
		Nodes: []dsl.DSLNode{
			node("start::1", dsl.KindStart, nil, nil),
			node("code::1", dsl.KindCode, map[string]any{
				"code": "sink({'doubled': inputs['n']*2})",
			}, []dsl.InputItem{refInput("n", "start::1", "n")}),
			node("end::1", dsl.KindEnd, map[string]any{
				"template": "result={{doubled}}",
			}, []dsl.InputItem{refInput("doubled", "code::1", "doubled")}),
		},
		Edges: []dsl.DSLEdge{
			{SourceNodeID: "start::1", TargetNodeID: "code::1"},
			{SourceNodeID: "code::1", TargetNodeID: "end::1"},
		},
	}

	result := runPlanWithRegistry(t, d, map[string]any{"n": 21.0}, reg)
	if got := result.Outputs["output"]; got != "result=42" {
		t.Fatalf("end output = %v, want result=42", got)
	}
	assertStatus(t, result, "code::1", nodes.StatusSucceeded)
}

func runPlan(t *testing.T, d *dsl.DSL, input map[string]any) *Result {
	t.Helper()
	return runPlanWithRegistry(t, d, input, nodes.NewDefaultRegistry(nodes.Config{}))
}

func runPlanWithRegistry(t *testing.T, d *dsl.DSL, input map[string]any, reg *nodes.Registry) *Result {
	t.Helper()
	plan, err := builder.Build(d)
	if err != nil {
		t.Fatalf("build plan: %v", err)
	}
	result, err := Run(context.Background(), plan, varpool.New(), Options{
		RunID:       "run_test",
		Input:       input,
		Concurrency: 4,
		Registry:    reg,
	})
	if err != nil {
		t.Fatalf("run plan: %v", err)
	}
	return result
}

func node(id, typ string, param map[string]any, inputs []dsl.InputItem) dsl.DSLNode {
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

func refInput(name, nodeID, port string) dsl.InputItem {
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

func assertStatus(t *testing.T, result *Result, nodeID, want string) {
	t.Helper()
	for _, n := range result.Nodes {
		if n.NodeID == nodeID {
			if n.Status != want {
				t.Fatalf("%s status = %s, want %s", nodeID, n.Status, want)
			}
			return
		}
	}
	t.Fatalf("node %s not found in result", nodeID)
}
