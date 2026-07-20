package builder

import (
	"fmt"

	"github.com/smart-workflow/smart-workflow/internal/dsl"
)

// Plan 是 DSL 编译出的可执行计划（设计文档 §10.6）。
// 执行依赖 = 显式 edges ∪ ref binding 推导的隐式边，
// 数据引用自动补一条控制依赖，杜绝引用了非 edge 上游却被提前调度。
type Plan struct {
	Nodes    map[string]dsl.DSLNode // dslNodeID -> 节点
	Edges    []dsl.DSLEdge          // 生效边集（含隐式 ref 边）
	InEdges  map[string][]dsl.DSLEdge
	OutEdges map[string][]dsl.DSLEdge
	StartIDs []string
	EndIDs   []string
}

// Build 把 DSL 编译成 Plan：建节点表 → 合并 edges∪ref → 建邻接 → 查环。
func Build(d *dsl.DSL) (*Plan, error) {
	nodes := make(map[string]dsl.DSLNode, len(d.Nodes))
	for _, n := range d.Nodes {
		if n.ID == "" {
			return nil, fmt.Errorf("build: node with empty id")
		}
		if _, dup := nodes[n.ID]; dup {
			return nil, fmt.Errorf("build: duplicate node id %q", n.ID)
		}
		nodes[n.ID] = n
	}

	// 生效边 = 显式边 + 隐式 ref 边（同一 (source,target) 去重，显式优先）。
	pair := func(s, t string) [2]string { return [2]string{s, t} }
	seen := make(map[[2]string]bool, len(d.Edges))
	edges := make([]dsl.DSLEdge, 0, len(d.Edges))

	for _, e := range d.Edges {
		if _, ok := nodes[e.SourceNodeID]; !ok {
			return nil, fmt.Errorf("build: edge source %q not found", e.SourceNodeID)
		}
		if _, ok := nodes[e.TargetNodeID]; !ok {
			return nil, fmt.Errorf("build: edge target %q not found", e.TargetNodeID)
		}
		edges = append(edges, e)
		seen[pair(e.SourceNodeID, e.TargetNodeID)] = true
	}

	for _, n := range d.Nodes {
		for _, in := range n.Data.Inputs {
			if in.Schema.Value.Type != dsl.DSLValueRef {
				continue
			}
			rc, ok := refContentOf(in.Schema.Value.Content)
			if !ok || rc.NodeID == "" || rc.NodeID == n.ID {
				continue
			}
			if _, ok := nodes[rc.NodeID]; !ok {
				return nil, fmt.Errorf("build: node %q ref missing source %q", n.ID, rc.NodeID)
			}
			if seen[pair(rc.NodeID, n.ID)] {
				continue
			}
			edges = append(edges, dsl.DSLEdge{SourceNodeID: rc.NodeID, TargetNodeID: n.ID})
			seen[pair(rc.NodeID, n.ID)] = true
		}
	}

	plan := &Plan{
		Nodes:    nodes,
		Edges:    edges,
		InEdges:  make(map[string][]dsl.DSLEdge, len(nodes)),
		OutEdges: make(map[string][]dsl.DSLEdge, len(nodes)),
	}
	for _, e := range edges {
		plan.OutEdges[e.SourceNodeID] = append(plan.OutEdges[e.SourceNodeID], e)
		plan.InEdges[e.TargetNodeID] = append(plan.InEdges[e.TargetNodeID], e)
	}

	for id, n := range nodes {
		switch n.Data.NodeMeta.NodeType {
		case dsl.KindStart:
			plan.StartIDs = append(plan.StartIDs, id)
		case dsl.KindEnd:
			plan.EndIDs = append(plan.EndIDs, id)
		}
	}

	if hasCycle(plan) {
		return nil, fmt.Errorf("build: workflow has a cycle")
	}
	return plan, nil
}

// hasCycle 用 Kahn 拓扑排序检测生效边集是否有环。
func hasCycle(p *Plan) bool {
	indeg := make(map[string]int, len(p.Nodes))
	for id := range p.Nodes {
		indeg[id] = len(p.InEdges[id])
	}
	queue := make([]string, 0)
	for id, d := range indeg {
		if d == 0 {
			queue = append(queue, id)
		}
	}
	visited := 0
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		visited++
		for _, e := range p.OutEdges[cur] {
			indeg[e.TargetNodeID]--
			if indeg[e.TargetNodeID] == 0 {
				queue = append(queue, e.TargetNodeID)
			}
		}
	}
	return visited != len(p.Nodes)
}

// refContentOf 兼容两种来源：内存构造的 dsl.RefContent 结构，
// 以及从 JSON 反序列化得到的 map[string]any（nodeId/name）。
func refContentOf(content any) (dsl.RefContent, bool) {
	switch c := content.(type) {
	case dsl.RefContent:
		return c, true
	case *dsl.RefContent:
		if c == nil {
			return dsl.RefContent{}, false
		}
		return *c, true
	case map[string]any:
		rc := dsl.RefContent{}
		if v, ok := c["nodeId"].(string); ok {
			rc.NodeID = v
		}
		if v, ok := c["name"].(string); ok {
			rc.Name = v
		}
		return rc, rc.NodeID != ""
	default:
		return dsl.RefContent{}, false
	}
}
