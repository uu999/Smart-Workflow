package validator

import (
	"fmt"

	"github.com/smart-workflow/smart-workflow/internal/dsl"
)

// Severity 区分必须修复的错误与建议性告警。
type Severity string

const (
	SeverityError   Severity = "error"
	SeverityWarning Severity = "warning"
)

// Issue 是一条结构化校验问题，供 CLI/画布逐点定位、供 Agent 自动修复。
type Issue struct {
	Code     string   `json:"code"`
	Severity Severity `json:"severity"`
	Node     string   `json:"node,omitempty"`
	Field    string   `json:"field,omitempty"`
	Message  string   `json:"message"`
}

// Result 汇总校验结果。
type Result struct {
	Issues []Issue `json:"issues"`
}

// HasError 是否存在 error 级问题（Agent 必须修复至 0）。
func (r *Result) HasError() bool {
	for _, i := range r.Issues {
		if i.Severity == SeverityError {
			return true
		}
	}
	return false
}

func (r *Result) add(sev Severity, code, node, field, msg string) {
	r.Issues = append(r.Issues, Issue{Code: code, Severity: sev, Node: node, Field: field, Message: msg})
}

// supportedKinds 是当前引擎支持的节点类型集合。
var supportedKinds = map[string]bool{
	dsl.KindStart: true, dsl.KindEnd: true, dsl.KindApplication: true,
	dsl.KindDataset: true, dsl.KindWorkflow: true, dsl.KindCondition: true, dsl.KindCode: true,
}

// Validate 对编辑态 IR 做全面校验（设计文档 §11.2）。
func Validate(ir *dsl.IR) *Result {
	r := &Result{}

	nodeByID := indexNodes(ir, r)

	checkStartEnd(ir, r)
	checkKindAndFields(ir, r)
	checkEdges(ir, nodeByID, r)
	checkCycle(ir, r)
	checkBindings(ir, nodeByID, r)
	checkReachability(ir, nodeByID, r)

	return r
}

// indexNodes 建 id->node 索引，并检查重复 ID。
func indexNodes(ir *dsl.IR, r *Result) map[string]*dsl.Node {
	m := make(map[string]*dsl.Node, len(ir.Nodes))
	for i := range ir.Nodes {
		n := &ir.Nodes[i]
		if n.ID == "" {
			r.add(SeverityError, "empty_node_id", "", "", "node with empty id")
			continue
		}
		if _, dup := m[n.ID]; dup {
			r.add(SeverityError, "duplicate_node_id", n.ID, "", fmt.Sprintf("duplicate node id %q", n.ID))
			continue
		}
		m[n.ID] = n
	}
	return m
}

// checkStartEnd 校验有且仅一个 start、至少一个 end。
func checkStartEnd(ir *dsl.IR, r *Result) {
	var starts, ends int
	for _, n := range ir.Nodes {
		switch n.Kind {
		case dsl.KindStart:
			starts++
		case dsl.KindEnd:
			ends++
		}
	}
	if starts != 1 {
		r.add(SeverityError, "start_count", "", "", fmt.Sprintf("must have exactly 1 start node, got %d", starts))
	}
	if ends < 1 {
		r.add(SeverityError, "end_missing", "", "", "must have at least 1 end node")
	}
}

// checkKindAndFields 校验节点类型受支持，及字段与 kind 匹配（改②）。
func checkKindAndFields(ir *dsl.IR, r *Result) {
	for _, n := range ir.Nodes {
		if !supportedKinds[n.Kind] {
			r.add(SeverityError, "unsupported_node_type", n.ID, "kind",
				fmt.Sprintf("unsupported node type %q", n.Kind))
			continue
		}
		// field_not_allowed_for_kind：非对应类型不得携带专属 ID 字段。
		if n.AppID != "" && n.Kind != dsl.KindApplication {
			r.add(SeverityError, "field_not_allowed_for_kind", n.ID, "app_id",
				fmt.Sprintf("%s node must not set app_id", n.Kind))
		}
		if n.DatasetID != "" && n.Kind != dsl.KindDataset {
			r.add(SeverityError, "field_not_allowed_for_kind", n.ID, "dataset_id",
				fmt.Sprintf("%s node must not set dataset_id", n.Kind))
		}
		if n.WorkflowID != "" && n.Kind != dsl.KindWorkflow {
			r.add(SeverityError, "field_not_allowed_for_kind", n.ID, "workflow_id",
				fmt.Sprintf("%s node must not set workflow_id", n.Kind))
		}
		// 必备 ID 缺失。
		switch n.Kind {
		case dsl.KindApplication:
			if n.AppID == "" {
				r.add(SeverityError, "missing_app_id", n.ID, "app_id", "application node requires app_id")
			}
		case dsl.KindDataset:
			if n.DatasetID == "" {
				r.add(SeverityError, "missing_dataset_id", n.ID, "dataset_id", "dataset node requires dataset_id")
			}
		case dsl.KindWorkflow:
			if n.WorkflowID == "" {
				r.add(SeverityError, "missing_workflow_id", n.ID, "workflow_id", "workflow node requires workflow_id")
			}
		}
	}
}

// checkEdges 校验边端点存在、无自环。
func checkEdges(ir *dsl.IR, nodes map[string]*dsl.Node, r *Result) {
	for _, e := range ir.Edges {
		if _, ok := nodes[e.Source]; !ok {
			r.add(SeverityError, "bad_edge", e.Source, "", fmt.Sprintf("edge source %q not found", e.Source))
		}
		if _, ok := nodes[e.Target]; !ok {
			r.add(SeverityError, "bad_edge", e.Target, "", fmt.Sprintf("edge target %q not found", e.Target))
		}
		if e.Source == e.Target {
			r.add(SeverityError, "self_loop", e.Source, "", fmt.Sprintf("self loop on %q", e.Source))
		}
	}
}

// checkCycle 基于 edges∪bindings 依赖图检测环（改①）。
func checkCycle(ir *dsl.IR, r *Result) {
	if dsl.HasCycle(dsl.BuildDeps(ir)) {
		r.add(SeverityError, "cycle", "", "", "workflow graph has a cycle")
	}
}

// checkBindings 校验必填绑定、ref 指向存在且类型兼容（改③）、作用域（改⑤）。
func checkBindings(ir *dsl.IR, nodes map[string]*dsl.Node, r *Result) {
	for i := range ir.Nodes {
		n := &ir.Nodes[i]
		bound := make(map[string]bool, len(n.Bindings))
		for _, b := range n.Bindings {
			bound[b.Port] = true
			checkOneBinding(n, b, nodes, r)
		}
		// 必填输入必须绑定。
		for _, in := range n.Inputs {
			if in.Required && !bound[in.Name] {
				r.add(SeverityError, "required_not_bound", n.ID, in.Name,
					fmt.Sprintf("required input %q not bound", in.Name))
			}
		}
	}
}

func checkOneBinding(n *dsl.Node, b dsl.Binding, nodes map[string]*dsl.Node, r *Result) {
	mode := b.Mode
	if mode == "" {
		if b.SourceNode != "" {
			mode = dsl.BindModeRef
		} else {
			mode = dsl.BindModeLiteral
		}
	}
	if mode != dsl.BindModeRef {
		return
	}
	// 改⑤：MVP 阶段禁止跨作用域引用。
	if b.Scope != "" {
		r.add(SeverityError, "cross_scope_ref_forbidden", n.ID, b.Port,
			fmt.Sprintf("cross-scope ref (scope=%q) not supported in MVP", b.Scope))
		return
	}
	src, ok := nodes[b.SourceNode]
	if !ok {
		r.add(SeverityError, "ref_node_not_found", n.ID, b.Port,
			fmt.Sprintf("ref source node %q not found", b.SourceNode))
		return
	}
	out := src.FindOutput(b.SourcePort)
	if out == nil {
		r.add(SeverityError, "source_port_not_found", n.ID, b.Port,
			fmt.Sprintf("ref source port %q not found on node %q", b.SourcePort, b.SourceNode))
		return
	}
	// 类型兼容：目标 input 声明了类型时比对。
	if in := findInput(n, b.Port); in != nil && in.Type != "" && out.Type != "" && in.Type != out.Type {
		r.add(SeverityError, "type_mismatch", n.ID, b.Port,
			fmt.Sprintf("type mismatch: %s.%s is %s, target %s expects %s",
				b.SourceNode, b.SourcePort, out.Type, b.Port, in.Type))
	}
}

// checkReachability 校验除 start 外节点可达、除 end 外能到达 end。
func checkReachability(ir *dsl.IR, nodes map[string]*dsl.Node, r *Result) {
	if len(ir.Nodes) == 0 {
		return
	}
	// 用 deps（含隐式边）判断可达性。
	deps := dsl.BuildDeps(ir)

	// 正向邻接：pre -> [后继]
	next := make(map[string][]string)
	for node, pres := range deps {
		for _, p := range pres {
			next[p] = append(next[p], node)
		}
	}

	// 从 start 出发能到达的集合。
	var startID string
	for _, n := range ir.Nodes {
		if n.Kind == dsl.KindStart {
			startID = n.ID
			break
		}
	}
	if startID == "" {
		return // start 数量问题已在 checkStartEnd 报过
	}
	reachable := bfs(startID, next)
	for _, n := range ir.Nodes {
		if n.Kind == dsl.KindStart {
			continue
		}
		if !reachable[n.ID] {
			r.add(SeverityWarning, "unreachable_node", n.ID, "",
				fmt.Sprintf("node %q is not reachable from start", n.ID))
		}
	}
}

func bfs(start string, adj map[string][]string) map[string]bool {
	seen := map[string]bool{start: true}
	queue := []string{start}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for _, nx := range adj[cur] {
			if !seen[nx] {
				seen[nx] = true
				queue = append(queue, nx)
			}
		}
	}
	return seen
}

func findInput(n *dsl.Node, name string) *dsl.Port {
	for i := range n.Inputs {
		if n.Inputs[i].Name == name {
			return &n.Inputs[i]
		}
	}
	return nil
}
