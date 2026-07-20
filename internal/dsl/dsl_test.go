package dsl

import (
	"encoding/json"
	"testing"
)

// sampleIR 构造设计文档 §10.3 的情感评测工作流。
func sampleIR() *IR {
	return &IR{
		Meta: Meta{Name: "情感评测", ProjectID: "6970"},
		Nodes: []Node{
			{
				ID:      "start_0",
				Kind:    KindStart,
				Outputs: []Port{{Name: "query", Type: ValueTypeString, Required: true}},
			},
			{
				ID:      "app_1",
				Kind:    KindApplication,
				AppID:   "12345",
				Title:   "情感分类器",
				Inputs:  []Port{{Name: "text", Type: ValueTypeString, Required: true}},
				Outputs: []Port{{Name: "label", Type: ValueTypeString}, {Name: "confidence", Type: ValueTypeNumber}},
				Bindings: []Binding{
					{Port: "text", Mode: BindModeRef, SourceNode: "start_0", SourcePort: "query"},
				},
			},
			{
				ID:     "end_0",
				Kind:   KindEnd,
				Inputs: []Port{{Name: "label", Type: ValueTypeString}, {Name: "confidence", Type: ValueTypeNumber}},
				Bindings: []Binding{
					{Port: "label", Mode: BindModeRef, SourceNode: "app_1", SourcePort: "label"},
					{Port: "confidence", Mode: BindModeRef, SourceNode: "app_1", SourcePort: "confidence"},
				},
			},
		},
		Edges: []Edge{
			{Source: "start_0", Target: "app_1"},
			{Source: "app_1", Target: "end_0"},
		},
	}
}

// deterministic ID 生成器：kind::kind_N，便于断言。
func detGen() func(string) string {
	c := map[string]int{}
	return func(kind string) string {
		id := kind + "::" + kind + itoa(c[kind])
		c[kind]++
		return id
	}
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	digits := ""
	for i > 0 {
		digits = string(rune('0'+i%10)) + digits
		i /= 10
	}
	return digits
}

func TestRender_StartAppEnd(t *testing.T) {
	r := &Renderer{IDGen: detGen()}
	d, err := r.Render(sampleIR())
	if err != nil {
		t.Fatalf("render: %v", err)
	}

	if len(d.Nodes) != 3 {
		t.Fatalf("want 3 nodes, got %d", len(d.Nodes))
	}
	if len(d.Edges) != 2 {
		t.Fatalf("want 2 edges, got %d", len(d.Edges))
	}

	// app 节点：id 应为 application::application0，nodeParam.appId=12345。
	app := d.Nodes[1]
	if app.ID != "application::application0" {
		t.Errorf("app id = %q", app.ID)
	}
	if app.Data.NodeParam["appId"] != "12345" {
		t.Errorf("appId = %v", app.Data.NodeParam["appId"])
	}
	if app.Data.NodeMeta.AliasName != "情感分类器" {
		t.Errorf("alias = %q", app.Data.NodeMeta.AliasName)
	}

	// app.text 输入应内联成 ref，指向 start 的 dsl id。
	if len(app.Data.Inputs) != 1 {
		t.Fatalf("app inputs = %d", len(app.Data.Inputs))
	}
	val := app.Data.Inputs[0].Schema.Value
	if val.Type != DSLValueRef {
		t.Fatalf("text value type = %q", val.Type)
	}
	ref, ok := val.Content.(RefContent)
	if !ok {
		t.Fatalf("content not RefContent: %T", val.Content)
	}
	if ref.NodeID != "start::start0" || ref.Name != "query" {
		t.Errorf("ref = %+v", ref)
	}

	// retryConfig 默认值。
	if app.Data.RetryConfig.Timeout != 60 {
		t.Errorf("timeout = %v", app.Data.RetryConfig.Timeout)
	}

	// edge 应映射到 dsl id。
	if d.Edges[0].SourceNodeID != "start::start0" || d.Edges[0].TargetNodeID != "application::application0" {
		t.Errorf("edge0 = %+v", d.Edges[0])
	}
}

func TestRender_LiteralAndInferredMode(t *testing.T) {
	ir := &IR{
		Nodes: []Node{
			{ID: "start_0", Kind: KindStart, Outputs: []Port{{Name: "q", Type: ValueTypeString}}},
			{
				ID:     "app_1",
				Kind:   KindApplication,
				AppID:  "1",
				Inputs: []Port{{Name: "p_lit", Type: ValueTypeString}, {Name: "p_ref", Type: ValueTypeString}},
				Bindings: []Binding{
					{Port: "p_lit", Value: "hello"},                    // 无 mode 无 source → literal
					{Port: "p_ref", SourceNode: "start_0", SourcePort: "q"}, // 无 mode 有 source → ref
				},
			},
		},
	}
	r := &Renderer{IDGen: detGen()}
	d, err := r.Render(ir)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	app := d.Nodes[1]
	byName := map[string]Value{}
	for _, in := range app.Data.Inputs {
		byName[in.Name] = in.Schema.Value
	}
	if byName["p_lit"].Type != DSLValueLiteral || byName["p_lit"].Content != "hello" {
		t.Errorf("p_lit = %+v", byName["p_lit"])
	}
	if byName["p_ref"].Type != DSLValueRef {
		t.Errorf("p_ref mode = %q", byName["p_ref"].Type)
	}
}

func TestRender_DuplicateNodeID(t *testing.T) {
	ir := &IR{Nodes: []Node{{ID: "n", Kind: KindStart}, {ID: "n", Kind: KindEnd}}}
	if _, err := NewRenderer().Render(ir); err == nil {
		t.Fatal("want error on duplicate id")
	}
}

func TestRoundTrip_RenderThenToIR(t *testing.T) {
	r := &Renderer{IDGen: detGen()}
	d, err := r.Render(sampleIR())
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	back, err := ToIR(d, Meta{Name: "情感评测"})
	if err != nil {
		t.Fatalf("toIR: %v", err)
	}

	if len(back.Nodes) != 3 || len(back.Edges) != 2 {
		t.Fatalf("round-trip shape: %d nodes %d edges", len(back.Nodes), len(back.Edges))
	}

	// app 节点还原：可读 id application_0，app_id 回填，text 绑定还原为 ref。
	var app *Node
	for i := range back.Nodes {
		if back.Nodes[i].Kind == KindApplication {
			app = &back.Nodes[i]
		}
	}
	if app == nil {
		t.Fatal("app node missing after round-trip")
	}
	if app.ID != "application_0" {
		t.Errorf("readable id = %q", app.ID)
	}
	if app.AppID != "12345" {
		t.Errorf("app_id = %q", app.AppID)
	}
	if len(app.Bindings) != 1 || app.Bindings[0].SourceNode != "start_0" || app.Bindings[0].SourcePort != "query" {
		t.Errorf("bindings = %+v", app.Bindings)
	}
	// 输出端口声明还原。
	if app.FindOutput("label") == nil || app.FindOutput("confidence") == nil {
		t.Errorf("outputs not restored: %+v", app.Outputs)
	}
}

func TestBuildDeps_IncludesImplicitEdge(t *testing.T) {
	// 只有 binding、没有显式 edge：deps 仍应把 ref 补成依赖。
	ir := &IR{
		Nodes: []Node{
			{ID: "a", Kind: KindStart, Outputs: []Port{{Name: "x", Type: ValueTypeString}}},
			{
				ID:       "b",
				Kind:     KindApplication,
				Bindings: []Binding{{Port: "in", Mode: BindModeRef, SourceNode: "a", SourcePort: "x"}},
			},
		},
		// 故意不写 edges
	}
	deps := BuildDeps(ir)
	if len(deps["b"]) != 1 || deps["b"][0] != "a" {
		t.Errorf("implicit dep b->a missing: %+v", deps["b"])
	}
}

func TestBuildDeps_UnionAndDedup(t *testing.T) {
	deps := BuildDeps(sampleIR())
	// app_1 依赖 start_0（edge + ref 同时存在，应去重为 1 条）。
	if len(deps["app_1"]) != 1 || deps["app_1"][0] != "start_0" {
		t.Errorf("app_1 deps = %+v", deps["app_1"])
	}
	// end_0 依赖 app_1。
	if len(deps["end_0"]) != 1 || deps["end_0"][0] != "app_1" {
		t.Errorf("end_0 deps = %+v", deps["end_0"])
	}
}

func TestHasCycle(t *testing.T) {
	if HasCycle(BuildDeps(sampleIR())) {
		t.Error("sample should be acyclic")
	}

	cyclic := &IR{
		Nodes: []Node{{ID: "a", Kind: KindApplication}, {ID: "b", Kind: KindApplication}},
		Edges: []Edge{{Source: "a", Target: "b"}, {Source: "b", Target: "a"}},
	}
	if !HasCycle(BuildDeps(cyclic)) {
		t.Error("a<->b should be cyclic")
	}
}

// conditionIR 构造一个带 condition 分支的 IR：start → cond →(命中分支0)→ end。
func conditionIR() *IR {
	return &IR{
		Meta: Meta{Name: "cond-flow"},
		Nodes: []Node{
			{ID: "start_0", Kind: KindStart, Outputs: []Port{{Name: "score", Type: ValueTypeNumber}}},
			{
				ID:   "cond_0",
				Kind: KindCondition,
				Bindings: []Binding{
					{Port: "score", Mode: BindModeRef, SourceNode: "start_0", SourcePort: "score"},
				},
				Branches: []Branch{
					{Index: 0, Conditions: []Condition{
						{LeftNode: "start_0", LeftPort: "score", Comparator: "gte", Right: 0.8},
					}},
					{Index: 1, Conditions: []Condition{
						{LeftNode: "start_0", LeftPort: "score", Comparator: "lt", Right: 0.8, RightMode: "literal"},
					}},
				},
			},
			{ID: "end_0", Kind: KindEnd},
		},
		Edges: []Edge{
			{Source: "start_0", Target: "cond_0"},
			{Source: "cond_0", Target: "end_0", SourcePort: "0"},
		},
	}
}

// TestRender_ConditionBranchesEmitted 验证 condition 分支被渲染进 nodeParam，
// 且形态正是 ConditionExecutor 读取的 []any of map[string]any{index,conditions}。
// 这是风险4的回归防线：此前 branches 被静默丢弃，condition 节点执行期必报
// "has no branches"。
func TestRender_ConditionBranchesEmitted(t *testing.T) {
	r := &Renderer{IDGen: detGen()}
	d, err := r.Render(conditionIR())
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	var cond *DSLNode
	for i := range d.Nodes {
		if d.Nodes[i].Data.NodeMeta.NodeType == KindCondition {
			cond = &d.Nodes[i]
		}
	}
	if cond == nil {
		t.Fatal("condition node missing")
	}
	branches, ok := cond.Data.NodeParam["branches"].([]any)
	if !ok {
		t.Fatalf("branches not []any: %T", cond.Data.NodeParam["branches"])
	}
	if len(branches) != 2 {
		t.Fatalf("want 2 branches, got %d", len(branches))
	}
	b0, ok := branches[0].(map[string]any)
	if !ok {
		t.Fatalf("branch0 not map: %T", branches[0])
	}
	conds, ok := b0["conditions"].([]any)
	if !ok || len(conds) != 1 {
		t.Fatalf("branch0 conditions = %v", b0["conditions"])
	}
	c0 := conds[0].(map[string]any)
	// left_node 应翻译成 DSL 真实 ID（start::start0），而非可读 ID。
	if c0["left_node"] != "start::start0" {
		t.Errorf("left_node = %v, want start::start0", c0["left_node"])
	}
	if c0["comparator"] != "gte" || c0["left_port"] != "score" {
		t.Errorf("condition fields = %+v", c0)
	}
}

// TestRoundTrip_ConditionSurvivesJSON 验证 condition 分支能扛过
// IR→DSL→JSON→DSL→IR 全程无损（存储用 marshal/unmarshal，index 会变 float64）。
func TestRoundTrip_ConditionSurvivesJSON(t *testing.T) {
	r := &Renderer{IDGen: detGen()}
	d, err := r.Render(conditionIR())
	if err != nil {
		t.Fatalf("render: %v", err)
	}

	// 模拟落库读回：marshal + unmarshal，index/right 变 float64。
	raw, err := json.Marshal(d)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var d2 DSL
	if err := json.Unmarshal(raw, &d2); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	back, err := ToIR(&d2, Meta{Name: "cond-flow"})
	if err != nil {
		t.Fatalf("toIR: %v", err)
	}

	var cond *Node
	for i := range back.Nodes {
		if back.Nodes[i].Kind == KindCondition {
			cond = &back.Nodes[i]
		}
	}
	if cond == nil {
		t.Fatal("condition node missing after round-trip")
	}
	if len(cond.Branches) != 2 {
		t.Fatalf("want 2 branches after round-trip, got %d", len(cond.Branches))
	}
	if cond.Branches[0].Index != 0 || cond.Branches[1].Index != 1 {
		t.Errorf("branch indices = %d,%d", cond.Branches[0].Index, cond.Branches[1].Index)
	}
	c := cond.Branches[0].Conditions[0]
	// left_node 应翻回可读 ID start_0。
	if c.LeftNode != "start_0" {
		t.Errorf("left_node after round-trip = %q, want start_0", c.LeftNode)
	}
	if c.Comparator != "gte" || c.LeftPort != "score" {
		t.Errorf("condition fields lost: %+v", c)
	}
	if cond.Branches[1].Conditions[0].RightMode != "literal" {
		t.Errorf("right_mode lost: %+v", cond.Branches[1].Conditions[0])
	}
}

// TestRoundTrip_BatchSurvives 验证 batch 预留字段扛过 render↔ToIR 不被静默丢弃。
func TestRoundTrip_BatchSurvives(t *testing.T) {
	ir := &IR{
		Meta: Meta{Name: "batch-flow"},
		Nodes: []Node{
			{ID: "start_0", Kind: KindStart, Outputs: []Port{{Name: "items", Type: ValueTypeArray}}},
			{
				ID:    "app_1",
				Kind:  KindApplication,
				AppID: "1",
				Batch: &Batch{Enable: true, SourceNode: "start_0", SourcePort: "items", ItemName: "item", Size: 10},
			},
			{ID: "end_0", Kind: KindEnd},
		},
		Edges: []Edge{{Source: "start_0", Target: "app_1"}, {Source: "app_1", Target: "end_0"}},
	}
	r := &Renderer{IDGen: detGen()}
	d, err := r.Render(ir)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	raw, _ := json.Marshal(d)
	var d2 DSL
	if err := json.Unmarshal(raw, &d2); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	back, err := ToIR(&d2, Meta{})
	if err != nil {
		t.Fatalf("toIR: %v", err)
	}
	var app *Node
	for i := range back.Nodes {
		if back.Nodes[i].Kind == KindApplication {
			app = &back.Nodes[i]
		}
	}
	if app == nil || app.Batch == nil {
		t.Fatal("batch lost after round-trip")
	}
	if !app.Batch.Enable || app.Batch.SourceNode != "start_0" || app.Batch.SourcePort != "items" {
		t.Errorf("batch fields = %+v", app.Batch)
	}
	if app.Batch.ItemName != "item" || app.Batch.Size != 10 {
		t.Errorf("batch item/size = %+v", app.Batch)
	}
}
