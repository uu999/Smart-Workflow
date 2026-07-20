package cli

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/smart-workflow/smart-workflow/internal/dsl"
)

// newInitCmd: swf init --name --project-id [--from-dsl file] [--sid id]
// 建一个会话；--from-dsl 时把现有 DSL 反渲染成 IR 导入（复用优先的兜底入口）。
func newInitCmd(a *appCtx) *cobra.Command {
	var name, projectID, fromDSL, sid string
	cmd := &cobra.Command{
		Use:   "init",
		Short: "创建构建会话（可选 --from-dsl 导入现有 DSL）",
		RunE: func(_ *cobra.Command, _ []string) error {
			if name == "" {
				return a.emitErr(newErr("BAD_REQUEST", "--name is required", "swf init --name <工作流名> --project-id <id>"))
			}
			if sid == "" {
				sid = genSID()
			}
			meta := Meta{Name: name, ProjectID: projectID}

			s, err := NewSession(sid, meta)
			if err != nil {
				return a.emitErr(newErr("SESSION_EXISTS", err.Error(), "换一个 --sid 或删除旧会话"))
			}

			// --from-dsl：把现有 DSL 反渲染成 IR 作为初始编辑对象。
			if fromDSL != "" {
				raw, rerr := readFile(fromDSL)
				if rerr != nil {
					return a.emitErr(newErr("BAD_REQUEST", rerr.Error(), ""))
				}
				var d dsl.DSL
				if jerr := json.Unmarshal(raw, &d); jerr != nil {
					return a.emitErr(newErr("INVALID_JSON", fmt.Sprintf("parse --from-dsl: %v", jerr), ""))
				}
				ir, terr := dsl.ToIR(&d, dsl.Meta{Name: name, ProjectID: projectID, Source: fromDSL})
				if terr != nil {
					return a.emitErr(newErr("IMPORT_FAILED", terr.Error(), ""))
				}
				s.IR = ir
				s.Meta.Source = fromDSL
				if serr := s.Save(); serr != nil {
					return a.emitErr(serr)
				}
			}

			return a.emitOK(map[string]any{
				"sid":      s.ID,
				"dir":      s.Dir(),
				"name":     name,
				"node_num": len(s.IR.Nodes),
			})
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "工作流名称（必填）")
	cmd.Flags().StringVar(&projectID, "project-id", "", "项目 ID")
	cmd.Flags().StringVar(&fromDSL, "from-dsl", "", "从现有 DSL 文件导入")
	cmd.Flags().StringVar(&sid, "sid", "", "指定会话 ID（缺省自动生成）")
	return cmd
}

// newAddNodeCmd: swf add-node --sid --id --kind [--app-id --title --inputs --outputs]
// --inputs/--outputs 支持 "name:type[:required]" 逗号分隔（风险5：端口声明入口）。
func newAddNodeCmd(a *appCtx) *cobra.Command {
	var sid, id, kind, appID, datasetID, workflowID, title, inputs, outputs string
	cmd := &cobra.Command{
		Use:   "add-node",
		Short: "增量添加一个节点（含端口声明 --inputs/--outputs）",
		RunE: func(_ *cobra.Command, _ []string) error {
			s, err := mustLoad(a, sid)
			if err != nil {
				return err
			}
			if id == "" || kind == "" {
				return a.emitErr(newErr("BAD_REQUEST", "--id and --kind are required", "swf add-node --sid <sid> --id app_1 --kind application"))
			}
			if s.IR.FindNode(id) != nil {
				return a.emitErr(newErr("NODE_EXISTS", fmt.Sprintf("node %q already exists", id), "换一个 --id 或先 remove-node"))
			}
			in, perr := parsePorts(inputs)
			if perr != nil {
				return a.emitErr(newErr("BAD_REQUEST", "parse --inputs: "+perr.Error(), `格式: name:type[:required]，逗号分隔，如 "text:string:required"`))
			}
			out, perr := parsePorts(outputs)
			if perr != nil {
				return a.emitErr(newErr("BAD_REQUEST", "parse --outputs: "+perr.Error(), ""))
			}
			node := dsl.Node{
				ID: id, Kind: kind, Title: title,
				AppID: appID, DatasetID: datasetID, WorkflowID: workflowID,
				Inputs: in, Outputs: out,
			}
			s.IR.Nodes = append(s.IR.Nodes, node)
			if err := s.Save(); err != nil {
				return a.emitErr(err)
			}
			return a.emitOK(map[string]any{"sid": s.ID, "added_node": id, "node_num": len(s.IR.Nodes)})
		},
	}
	cmd.Flags().StringVar(&sid, "sid", "", "会话 ID（必填）")
	cmd.Flags().StringVar(&id, "id", "", "可读节点 ID，如 app_1（必填）")
	cmd.Flags().StringVar(&kind, "kind", "", "节点类型 start/end/application/dataset/workflow/condition/code（必填）")
	cmd.Flags().StringVar(&appID, "app-id", "", "application 节点的 app_id")
	cmd.Flags().StringVar(&datasetID, "dataset-id", "", "dataset 节点的 dataset_id")
	cmd.Flags().StringVar(&workflowID, "workflow-id", "", "workflow 节点的 workflow_id")
	cmd.Flags().StringVar(&title, "title", "", "节点显示名")
	cmd.Flags().StringVar(&inputs, "inputs", "", `输入端口声明，name:type[:required] 逗号分隔`)
	cmd.Flags().StringVar(&outputs, "outputs", "", `输出端口声明，name:type[:required] 逗号分隔`)
	return cmd
}

// newAddEdgeCmd: swf add-edge --sid --source --target [--source-port]
func newAddEdgeCmd(a *appCtx) *cobra.Command {
	var sid, source, target, sourcePort string
	cmd := &cobra.Command{
		Use:   "add-edge",
		Short: "增量添加一条控制流边",
		RunE: func(_ *cobra.Command, _ []string) error {
			s, err := mustLoad(a, sid)
			if err != nil {
				return err
			}
			if source == "" || target == "" {
				return a.emitErr(newErr("BAD_REQUEST", "--source and --target are required", "swf add-edge --sid <sid> --source start_0 --target app_1"))
			}
			if s.IR.FindNode(source) == nil {
				return a.emitErr(newErr("NODE_NOT_FOUND", fmt.Sprintf("source node %q not found", source), "先 add-node 再连边"))
			}
			if s.IR.FindNode(target) == nil {
				return a.emitErr(newErr("NODE_NOT_FOUND", fmt.Sprintf("target node %q not found", target), "先 add-node 再连边"))
			}
			s.IR.Edges = append(s.IR.Edges, dsl.Edge{Source: source, Target: target, SourcePort: sourcePort})
			if err := s.Save(); err != nil {
				return a.emitErr(err)
			}
			return a.emitOK(map[string]any{"sid": s.ID, "edge": map[string]string{"source": source, "target": target}, "edge_num": len(s.IR.Edges)})
		},
	}
	cmd.Flags().StringVar(&sid, "sid", "", "会话 ID（必填）")
	cmd.Flags().StringVar(&source, "source", "", "源节点 ID（必填）")
	cmd.Flags().StringVar(&target, "target", "", "目标节点 ID（必填）")
	cmd.Flags().StringVar(&sourcePort, "source-port", "", "condition 分支序号（可选）")
	return cmd
}

// newBindCmd: swf bind --sid --node-id --port --mode ref|literal --source-node --source-port | --value
func newBindCmd(a *appCtx) *cobra.Command {
	var sid, nodeID, port, mode, sourceNode, sourcePort, value string
	cmd := &cobra.Command{
		Use:   "bind",
		Short: "把节点输入端口绑定到上游输出(ref)或字面量(literal)",
		RunE: func(_ *cobra.Command, _ []string) error {
			s, err := mustLoad(a, sid)
			if err != nil {
				return err
			}
			if nodeID == "" || port == "" {
				return a.emitErr(newErr("BAD_REQUEST", "--node-id and --port are required", "swf bind --sid <sid> --node-id app_1 --port text --mode ref --source-node start_0 --source-port query"))
			}
			node := s.IR.FindNode(nodeID)
			if node == nil {
				return a.emitErr(newErr("NODE_NOT_FOUND", fmt.Sprintf("node %q not found", nodeID), ""))
			}
			b := dsl.Binding{Port: port, Mode: mode, SourceNode: sourceNode, SourcePort: sourcePort}
			if mode == dsl.BindModeLiteral || (mode == "" && sourceNode == "") {
				// literal：把 --value 当 JSON 解析，失败则按原始字符串。
				var v any
				if value != "" {
					if json.Unmarshal([]byte(value), &v) != nil {
						v = value
					}
				}
				b.Mode = dsl.BindModeLiteral
				b.Value = v
			}
			// 同端口重复绑定则覆盖（增量编辑友好）。
			replaced := false
			for i := range node.Bindings {
				if node.Bindings[i].Port == port {
					node.Bindings[i] = b
					replaced = true
					break
				}
			}
			if !replaced {
				node.Bindings = append(node.Bindings, b)
			}
			if err := s.Save(); err != nil {
				return a.emitErr(err)
			}
			return a.emitOK(map[string]any{"sid": s.ID, "node_id": nodeID, "port": port, "mode": b.Mode, "replaced": replaced})
		},
	}
	cmd.Flags().StringVar(&sid, "sid", "", "会话 ID（必填）")
	cmd.Flags().StringVar(&nodeID, "node-id", "", "目标节点 ID（必填）")
	cmd.Flags().StringVar(&port, "port", "", "目标输入端口名（必填）")
	cmd.Flags().StringVar(&mode, "mode", "", "ref/literal（缺省：有 source-node 判 ref，否则 literal）")
	cmd.Flags().StringVar(&sourceNode, "source-node", "", "ref: 上游节点 ID")
	cmd.Flags().StringVar(&sourcePort, "source-port", "", "ref: 上游输出端口名")
	cmd.Flags().StringVar(&value, "value", "", "literal: 字面量（尝试按 JSON 解析）")
	return cmd
}

// newRemoveNodeCmd: swf remove-node --sid --id （同时清理关联边与对该节点的绑定）
func newRemoveNodeCmd(a *appCtx) *cobra.Command {
	var sid, id string
	cmd := &cobra.Command{
		Use:   "remove-node",
		Short: "删除节点，并清理其关联边与对它的 ref 绑定",
		RunE: func(_ *cobra.Command, _ []string) error {
			s, err := mustLoad(a, sid)
			if err != nil {
				return err
			}
			if id == "" {
				return a.emitErr(newErr("BAD_REQUEST", "--id is required", ""))
			}
			if s.IR.FindNode(id) == nil {
				return a.emitErr(newErr("NODE_NOT_FOUND", fmt.Sprintf("node %q not found", id), ""))
			}
			// 删节点。
			nodes := make([]dsl.Node, 0, len(s.IR.Nodes))
			for _, n := range s.IR.Nodes {
				if n.ID == id {
					continue
				}
				// 清理指向被删节点的 ref 绑定。
				kept := make([]dsl.Binding, 0, len(n.Bindings))
				for _, b := range n.Bindings {
					if b.SourceNode == id {
						continue
					}
					kept = append(kept, b)
				}
				n.Bindings = kept
				nodes = append(nodes, n)
			}
			s.IR.Nodes = nodes
			// 清理关联边。
			edges := make([]dsl.Edge, 0, len(s.IR.Edges))
			for _, e := range s.IR.Edges {
				if e.Source == id || e.Target == id {
					continue
				}
				edges = append(edges, e)
			}
			s.IR.Edges = edges
			if err := s.Save(); err != nil {
				return a.emitErr(err)
			}
			return a.emitOK(map[string]any{"sid": s.ID, "removed_node": id, "node_num": len(s.IR.Nodes), "edge_num": len(s.IR.Edges)})
		},
	}
	cmd.Flags().StringVar(&sid, "sid", "", "会话 ID（必填）")
	cmd.Flags().StringVar(&id, "id", "", "要删除的节点 ID（必填）")
	return cmd
}

// newRemoveEdgeCmd: swf remove-edge --sid --source --target
func newRemoveEdgeCmd(a *appCtx) *cobra.Command {
	var sid, source, target string
	cmd := &cobra.Command{
		Use:   "remove-edge",
		Short: "删除一条控制流边",
		RunE: func(_ *cobra.Command, _ []string) error {
			s, err := mustLoad(a, sid)
			if err != nil {
				return err
			}
			if source == "" || target == "" {
				return a.emitErr(newErr("BAD_REQUEST", "--source and --target are required", ""))
			}
			edges := make([]dsl.Edge, 0, len(s.IR.Edges))
			removed := 0
			for _, e := range s.IR.Edges {
				if e.Source == source && e.Target == target {
					removed++
					continue
				}
				edges = append(edges, e)
			}
			if removed == 0 {
				return a.emitErr(newErr("EDGE_NOT_FOUND", fmt.Sprintf("edge %s->%s not found", source, target), ""))
			}
			s.IR.Edges = edges
			if err := s.Save(); err != nil {
				return a.emitErr(err)
			}
			return a.emitOK(map[string]any{"sid": s.ID, "removed_edge": map[string]string{"source": source, "target": target}, "edge_num": len(s.IR.Edges)})
		},
	}
	cmd.Flags().StringVar(&sid, "sid", "", "会话 ID（必填）")
	cmd.Flags().StringVar(&source, "source", "", "源节点 ID（必填）")
	cmd.Flags().StringVar(&target, "target", "", "目标节点 ID（必填）")
	return cmd
}

// newRemoveBindingCmd: swf remove-binding --sid --node-id --port （对齐设计的 remove-port）
func newRemoveBindingCmd(a *appCtx) *cobra.Command {
	var sid, nodeID, port string
	cmd := &cobra.Command{
		Use:   "remove-binding",
		Short: "删除某节点某端口的绑定",
		RunE: func(_ *cobra.Command, _ []string) error {
			s, err := mustLoad(a, sid)
			if err != nil {
				return err
			}
			if nodeID == "" || port == "" {
				return a.emitErr(newErr("BAD_REQUEST", "--node-id and --port are required", ""))
			}
			node := s.IR.FindNode(nodeID)
			if node == nil {
				return a.emitErr(newErr("NODE_NOT_FOUND", fmt.Sprintf("node %q not found", nodeID), ""))
			}
			kept := make([]dsl.Binding, 0, len(node.Bindings))
			removed := 0
			for _, b := range node.Bindings {
				if b.Port == port {
					removed++
					continue
				}
				kept = append(kept, b)
			}
			if removed == 0 {
				return a.emitErr(newErr("BINDING_NOT_FOUND", fmt.Sprintf("no binding on port %q", port), ""))
			}
			node.Bindings = kept
			if err := s.Save(); err != nil {
				return a.emitErr(err)
			}
			return a.emitOK(map[string]any{"sid": s.ID, "node_id": nodeID, "removed_port": port})
		},
	}
	cmd.Flags().StringVar(&sid, "sid", "", "会话 ID（必填）")
	cmd.Flags().StringVar(&nodeID, "node-id", "", "节点 ID（必填）")
	cmd.Flags().StringVar(&port, "port", "", "要解绑的端口名（必填）")
	return cmd
}

// parsePorts 解析 "name:type[:required]" 逗号分隔的端口声明。空串返回 nil。
func parsePorts(s string) ([]dsl.Port, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, nil
	}
	var ports []dsl.Port
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		fields := strings.Split(part, ":")
		if len(fields) < 2 || fields[0] == "" || fields[1] == "" {
			return nil, fmt.Errorf("bad port spec %q (want name:type[:required])", part)
		}
		p := dsl.Port{Name: fields[0], Type: fields[1]}
		if len(fields) >= 3 && (fields[2] == "required" || fields[2] == "true") {
			p.Required = true
		}
		ports = append(ports, p)
	}
	return ports, nil
}
