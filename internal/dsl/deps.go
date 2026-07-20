package dsl

import "sort"

// BuildDeps 构建执行依赖图（设计文档 §10.6）：
//
//	deps = 显式 edges ∪ 由 ref bindings 推导的隐式边
//
// 返回 map[nodeID] -> 去重且排序的前驱节点 ID 列表（IR 可读 ID）。
// 数据引用自动补一条控制依赖，杜绝"引用了非 edge 上游节点却被提前调度"。
func BuildDeps(ir *IR) map[string][]string {
	set := make(map[string]map[string]struct{}, len(ir.Nodes))
	ensure := func(id string) {
		if set[id] == nil {
			set[id] = make(map[string]struct{})
		}
	}
	for _, n := range ir.Nodes {
		ensure(n.ID)
	}

	// 显式控制流：edge.target 依赖 edge.source。
	for _, e := range ir.Edges {
		ensure(e.Target)
		set[e.Target][e.Source] = struct{}{}
	}

	// 隐式数据流：ref binding 的 target 节点依赖 source_node。
	for _, n := range ir.Nodes {
		for _, b := range n.Bindings {
			if b.resolvedMode() == BindModeRef && b.SourceNode != "" {
				ensure(n.ID)
				set[n.ID][b.SourceNode] = struct{}{}
			}
		}
	}

	out := make(map[string][]string, len(set))
	for id, deps := range set {
		list := make([]string, 0, len(deps))
		for d := range deps {
			list = append(list, d)
		}
		sort.Strings(list)
		out[id] = list
	}
	return out
}

// HasCycle 基于依赖图做 Kahn 拓扑排序检测环，返回是否有环。
func HasCycle(deps map[string][]string) bool {
	indeg := make(map[string]int, len(deps))
	for n := range deps {
		indeg[n] = 0
	}
	// deps[n] 是 n 的前驱；边方向 pre -> n，故 n 的入度 = len(deps[n])。
	for n, pres := range deps {
		indeg[n] = len(pres)
	}

	// 反向邻接：pre -> [后继...]
	next := make(map[string][]string, len(deps))
	for n, pres := range deps {
		for _, p := range pres {
			next[p] = append(next[p], n)
		}
	}

	queue := make([]string, 0)
	for n, d := range indeg {
		if d == 0 {
			queue = append(queue, n)
		}
	}

	visited := 0
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		visited++
		for _, nx := range next[cur] {
			indeg[nx]--
			if indeg[nx] == 0 {
				queue = append(queue, nx)
			}
		}
	}
	return visited != len(indeg)
}
