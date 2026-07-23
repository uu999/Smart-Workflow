package cli

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/smart-workflow/smart-workflow/internal/dsl"
)

// planFile 是 plan-apply 接受的声明式建图 JSON（对齐 Byteval plan.json）。
// 一份 plan 一次性描述整张图：workflow 元数据 + nodes[]（内联 bindings[]）+ edges[]，
// 替代逐条 add-node/add-edge/bind。
type planFile struct {
	Workflow planWorkflow `json:"workflow"`
	Nodes    []planNode   `json:"nodes"`
	Edges    []planEdge   `json:"edges"`
}

type planWorkflow struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	ProjectID   string `json:"project_id"`
}

type planNode struct {
	ID         string         `json:"id"`
	Kind       string         `json:"kind"`
	Title      string         `json:"title"`
	AppID      string         `json:"app_id"`
	DatasetID  string         `json:"dataset_id"`
	WorkflowID string         `json:"workflow_id"`
	Inputs     []planPort     `json:"inputs"`
	Outputs    []planPort     `json:"outputs"`
	Bindings   []planBinding  `json:"bindings"`
	Batch      *planBatch     `json:"batch"`  // 逐项执行（分类器对 dataset rows 逐条跑）
	Params     map[string]any `json:"params"` // 节点私有参数（code 节点的 code、prompt 等）
}

// planBatch 对齐 IR dsl.Batch：对某个数组输入端口逐项执行底层执行器并聚合。
type planBatch struct {
	Enable     bool   `json:"enable"`
	SourceNode string `json:"source_node"`
	SourcePort string `json:"source_port"`
	ItemName   string `json:"item_name"`
	Size       int    `json:"size"`
}

type planPort struct {
	Name     string `json:"name"`
	Type     string `json:"type"`
	Required bool   `json:"required"`
}

type planBinding struct {
	Port       string `json:"port"`
	Mode       string `json:"mode"` // ref/literal/clear；缺省时有 source_node 推 ref，否则 literal
	SourceNode string `json:"source_node"`
	SourcePort string `json:"source_port"`
	Value      any    `json:"value"`
}

type planEdge struct {
	Source     string `json:"source"`
	Target     string `json:"target"`
	SourcePort string `json:"source_port"`
}

// newPlanApplyCmd: swf plan-apply --sid --file plan.json
// 声明式建图：吃一份完整 JSON plan，批量落成 IR session 的节点/边/绑定。
// 语义对齐 Byteval「plan apply 按 JSON plan 批量创建 session 草稿」。
//
// 注意：这是构建期能力（生成图），不是运行期「批量起 run」。
func newPlanApplyCmd(a *appCtx) *cobra.Command {
	var sid, file string
	cmd := &cobra.Command{
		Use:   "plan-apply",
		Short: "声明式建图：按 JSON plan 批量落节点/边/绑定到会话",
		RunE: func(_ *cobra.Command, _ []string) error {
			if file == "" {
				return a.emitErr(newErr("BAD_REQUEST", "--file is required", "swf plan-apply --sid <sid> --file plan.json"))
			}
			raw, rerr := readFile(file)
			if rerr != nil {
				return a.emitErr(newErr("BAD_REQUEST", rerr.Error(), ""))
			}
			var plan planFile
			if jerr := json.Unmarshal(raw, &plan); jerr != nil {
				return a.emitErr(newErr("INVALID_JSON", fmt.Sprintf("parse plan: %v", jerr), "参考 swf plan-schema 的节点规格"))
			}

			// --sid 缺省时用 plan.workflow 元数据新建会话；给了则加载既有会话。
			s, err := loadOrInitPlanSession(a, sid, plan.Workflow)
			if err != nil {
				return a.emitErr(err)
			}

			if verr := applyPlan(s.IR, &plan); verr != nil {
				return a.emitErr(verr)
			}
			if serr := s.Save(); serr != nil {
				return a.emitErr(serr)
			}
			return a.emitOK(map[string]any{
				"sid":      s.ID,
				"node_num": len(s.IR.Nodes),
				"edge_num": len(s.IR.Edges),
			})
		},
	}
	cmd.Flags().StringVar(&sid, "sid", "", "会话 ID（缺省用 plan.workflow 新建）")
	cmd.Flags().StringVar(&file, "file", "", "plan.json 路径（必填）")
	return cmd
}

// loadOrInitPlanSession：给了 --sid 加载既有会话；否则用 plan.workflow 元数据新建。
func loadOrInitPlanSession(a *appCtx, sid string, wf planWorkflow) (*Session, error) {
	if sid != "" {
		return mustLoad(a, sid)
	}
	newSID := genSID()
	s, err := NewSession(newSID, Meta{Name: wf.Name, ProjectID: wf.ProjectID})
	if err != nil {
		return nil, newErr("SESSION_EXISTS", err.Error(), "换一个 --sid 或删除旧会话")
	}
	return s, nil
}

// applyPlan 把 plan 的节点/边/绑定落进 IR，做基本校验：
// 节点 id 非空且不重复；边端点必须存在；绑定挂到对应节点。
func applyPlan(ir *dsl.IR, plan *planFile) error {
	// 1) 节点：校验 id 唯一（含与会话已有节点去重）。
	seen := map[string]bool{}
	for _, n := range ir.Nodes {
		seen[n.ID] = true
	}
	for _, pn := range plan.Nodes {
		if pn.ID == "" || pn.Kind == "" {
			return newErr("BAD_PLAN", "each node needs non-empty id and kind", "")
		}
		if seen[pn.ID] {
			return newErr("NODE_EXISTS", fmt.Sprintf("duplicate node id %q", pn.ID), "")
		}
		seen[pn.ID] = true
		ir.Nodes = append(ir.Nodes, planNodeToIR(pn))
	}

	// 2) 边：端点必须存在（在合并后的节点集合里）。
	for _, pe := range plan.Edges {
		if !seen[pe.Source] {
			return newErr("NODE_NOT_FOUND", fmt.Sprintf("edge source %q not found", pe.Source), "")
		}
		if !seen[pe.Target] {
			return newErr("NODE_NOT_FOUND", fmt.Sprintf("edge target %q not found", pe.Target), "")
		}
		ir.Edges = append(ir.Edges, dsl.Edge{Source: pe.Source, Target: pe.Target, SourcePort: pe.SourcePort})
	}
	return nil
}

// planNodeToIR 把 plan 节点转成 IR 节点（含内联 bindings、端口、batch、params）。
func planNodeToIR(pn planNode) dsl.Node {
	node := dsl.Node{
		ID: pn.ID, Kind: pn.Kind, Title: pn.Title,
		AppID: pn.AppID, DatasetID: pn.DatasetID, WorkflowID: pn.WorkflowID,
		Inputs:  planPortsToIR(pn.Inputs),
		Outputs: planPortsToIR(pn.Outputs),
		Params:  pn.Params,
	}
	for _, pb := range pn.Bindings {
		node.Bindings = append(node.Bindings, planBindingToIR(pb))
	}
	// batch：逐项执行（render 会写进 nodeParam["batch"]，scheduler.batchOf 读取）。
	if pn.Batch != nil && pn.Batch.Enable {
		node.Batch = &dsl.Batch{
			Enable:     true,
			SourceNode: pn.Batch.SourceNode,
			SourcePort: pn.Batch.SourcePort,
			ItemName:   pn.Batch.ItemName,
			Size:       pn.Batch.Size,
		}
	}
	return node
}

func planPortsToIR(ports []planPort) []dsl.Port {
	if len(ports) == 0 {
		return nil
	}
	out := make([]dsl.Port, 0, len(ports))
	for _, p := range ports {
		out = append(out, dsl.Port{Name: p.Name, Type: p.Type, Required: p.Required})
	}
	return out
}

// planBindingToIR 转换绑定，mode 缺省推断（对齐 Byteval：有 source_node 推 ref，否则 literal）。
func planBindingToIR(pb planBinding) dsl.Binding {
	mode := pb.Mode
	if mode == "" {
		if pb.SourceNode != "" {
			mode = dsl.BindModeRef
		} else {
			mode = dsl.BindModeLiteral
		}
	}
	return dsl.Binding{
		Port:       pb.Port,
		Mode:       mode,
		SourceNode: pb.SourceNode,
		SourcePort: pb.SourcePort,
		Value:      pb.Value,
	}
}

// newPlanSchemaCmd: swf plan-schema
// 打印 plan-apply 接受的 plan.json 规格（对齐 Byteval plan schema，供 Agent 自检字段拼写）。
func newPlanSchemaCmd(a *appCtx) *cobra.Command {
	return &cobra.Command{
		Use:   "plan-schema",
		Short: "打印 plan-apply 的 plan.json 规格（各 kind 节点字段）",
		RunE: func(_ *cobra.Command, _ []string) error {
			return a.emitOK(map[string]any{
				"workflow": map[string]string{
					"name": "string", "description": "string", "project_id": "string",
				},
				"nodes[]": map[string]any{
					"id":          "string（必填，可读 ID 如 app_1）",
					"kind":        "start|end|application|dataset|workflow|condition|code（必填）",
					"title":       "string（可选，显示名）",
					"app_id":      "application 节点必填",
					"dataset_id":  "dataset 节点必填",
					"workflow_id": "workflow 节点必填",
					"inputs":      `[{"name","type","required"}]`,
					"outputs":     `[{"name","type","required"}]`,
					"bindings":    `[{"port","mode":"ref|literal|clear","source_node","source_port","value"}]（mode 缺省：有 source_node 推 ref，否则 literal）`,
					"batch":       `{"enable":true,"source_node","source_port","item_name","size"}（逐项执行：对某数组输入逐条跑底层执行器，聚合为 {items,count}）`,
					"params":      `{...}（节点私有参数：code 节点放 {"code":"...python..."}，prompt/template 等）`,
				},
				"edges[]": map[string]string{
					"source": "源节点 id", "target": "目标节点 id",
					"source_port": "condition 分支序号（可选）",
				},
				"note": "start、end 各仅一个节点；输出由 end 节点绑定决定",
			})
		},
	}
}
