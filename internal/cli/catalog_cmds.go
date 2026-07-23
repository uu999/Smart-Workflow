package cli

import (
	"encoding/json"
	"fmt"
	"net/url"

	"github.com/spf13/cobra"

	"github.com/smart-workflow/smart-workflow/internal/catalog"
	"github.com/smart-workflow/smart-workflow/internal/dsl"
)

// newSearchCmd: swf search --kind application|workflow --name --project-id
// 能力发现第一环（把"猜"变"选"）：搜可复用的 application / workflow。
// dataset 在 M8 明确不支持（无存储+无执行器，缓期到 M10）。
func newSearchCmd(a *appCtx) *cobra.Command {
	var kind, name, projectID string
	var limit int
	cmd := &cobra.Command{
		Use:   "search",
		Short: "搜索可复用能力（application / workflow）",
		RunE: func(_ *cobra.Command, _ []string) error {
			if projectID == "" {
				return a.emitErr(newErr("BAD_REQUEST", "--project-id is required", "swf search --kind application --name qwen --project-id 6970"))
			}
			var path string
			switch kind {
			case dsl.KindApplication, "app":
				path = "/v1/applications"
			case dsl.KindWorkflow, "wf":
				path = "/v1/workflows"
			case dsl.KindDataset:
				return a.emitErr(newErr("NOT_SUPPORTED",
					"dataset search is not available via `search`",
					"评测集用专用命令：swf dataset-list --project-id <id> [--name 关键词]"))
			default:
				return a.emitErr(newErr("BAD_REQUEST",
					fmt.Sprintf("unknown --kind %q", kind),
					"--kind application 或 --kind workflow"))
			}

			q := url.Values{}
			q.Set("project_id", projectID)
			q.Set("name", name) // 存在即触发服务端模糊搜索；空串匹配全部
			if limit > 0 {
				q.Set("limit", fmt.Sprintf("%d", limit))
			}
			var resp struct {
				Items json.RawMessage `json:"items"`
			}
			if err := a.client().doJSON("GET", path+"?"+q.Encode(), nil, &resp); err != nil {
				return a.emitErr(err)
			}
			return a.emitOK(map[string]any{"kind": kind, "items": resp.Items})
		},
	}
	cmd.Flags().StringVar(&kind, "kind", "application", "application / workflow")
	cmd.Flags().StringVar(&name, "name", "", "名称模糊匹配（空=全部）")
	cmd.Flags().StringVar(&projectID, "project-id", "", "项目 ID（必填）")
	cmd.Flags().IntVar(&limit, "limit", 0, "返回条数上限")
	return cmd
}

// newAppSchemaCmd: swf app-schema --sid --app-id [--project-id]
// 拉取应用 schema，解析成端口，① 缓存原文到 app_cache/<id>.json ② 若 session 里
// 已有该 app 节点，把端口物化进它的 Inputs/Outputs（决策Q1：cache 是记录，IR 是真相）。
func newAppSchemaCmd(a *appCtx) *cobra.Command {
	var sid, appID, projectID string
	cmd := &cobra.Command{
		Use:   "app-schema",
		Short: "拉取并缓存应用 schema；若已加该节点则把端口物化进 IR",
		RunE: func(_ *cobra.Command, _ []string) error {
			s, err := mustLoad(a, sid)
			if err != nil {
				return err
			}
			if appID == "" {
				return a.emitErr(newErr("BAD_REQUEST", "--app-id is required", ""))
			}
			// 拉应用详情（含 input_schema/output_schema 原文）。
			var app struct {
				AppID        string          `json:"app_id"`
				Name         string          `json:"name"`
				InputSchema  json.RawMessage `json:"input_schema"`
				OutputSchema json.RawMessage `json:"output_schema"`
			}
			if derr := a.client().doJSON("GET", "/v1/applications/"+appID, nil, &app); derr != nil {
				return a.emitErr(derr)
			}
			sch, perr := catalog.ParseAppSchema(app.AppID, app.Name, app.InputSchema, app.OutputSchema)
			if perr != nil {
				return a.emitErr(newErr("BAD_SCHEMA", perr.Error(),
					`app schema 应为端口数组 [{"name","type","required"}]`))
			}

			// ① 缓存原文（记录用，供 clone / 审计）。
			if werr := s.WriteAppCache(appID, app); werr != nil {
				return a.emitErr(werr)
			}

			// ② 若 session 里已有该 app 节点，物化端口进 IR。
			materialized := false
			for i := range s.IR.Nodes {
				n := &s.IR.Nodes[i]
				if n.Kind == dsl.KindApplication && n.AppID == appID {
					n.Inputs = sch.Inputs
					n.Outputs = sch.Outputs
					materialized = true
				}
			}
			if materialized {
				if serr := s.Save(); serr != nil {
					return a.emitErr(serr)
				}
			}

			return a.emitOK(map[string]any{
				"sid":          s.ID,
				"app_id":       appID,
				"name":         sch.Name,
				"inputs":       sch.Inputs,
				"outputs":      sch.Outputs,
				"materialized": materialized, // 是否已回填进 IR 节点
				"cache_path":   s.AppCachePath(appID),
			})
		},
	}
	cmd.Flags().StringVar(&sid, "sid", "", "会话 ID（必填）")
	cmd.Flags().StringVar(&appID, "app-id", "", "应用 ID（必填）")
	cmd.Flags().StringVar(&projectID, "project-id", "", "项目 ID（可选，仅记录）")
	return cmd
}

// newScopeCmd: swf scope --sid --node-id --port
// 能力发现最精妙一环：按「上游连通性 + ValueType」过滤出目标端口的合法可绑定候选，
// 把"在整张图里猜端口"变成"从候选里选"。渲染不做自动匹配，先 scope 再 bind。
func newScopeCmd(a *appCtx) *cobra.Command {
	var sid, nodeID, port string
	cmd := &cobra.Command{
		Use:   "scope",
		Short: "列出某输入端口此刻可绑定的上游输出候选（连通性+类型过滤）",
		RunE: func(_ *cobra.Command, _ []string) error {
			s, err := mustLoad(a, sid)
			if err != nil {
				return err
			}
			if nodeID == "" || port == "" {
				return a.emitErr(newErr("BAD_REQUEST", "--node-id and --port are required", ""))
			}
			target := s.IR.FindNode(nodeID)
			if target == nil {
				return a.emitErr(newErr("NODE_NOT_FOUND", fmt.Sprintf("node %q not found", nodeID), ""))
			}

			// 目标端口声明的类型（可能为空 = 未知，不做类型过滤）。
			wantType := ""
			if p := findPort(target.Inputs, port); p != nil {
				wantType = p.Type
			}

			candidates := scopeCandidates(s.IR, nodeID, wantType)
			return a.emitOK(map[string]any{
				"sid":        s.ID,
				"node_id":    nodeID,
				"port":       port,
				"want_type":  wantType,
				"candidates": candidates,
			})
		},
	}
	cmd.Flags().StringVar(&sid, "sid", "", "会话 ID（必填）")
	cmd.Flags().StringVar(&nodeID, "node-id", "", "目标节点 ID（必填）")
	cmd.Flags().StringVar(&port, "port", "", "目标输入端口名（必填）")
	return cmd
}

// scopeCandidate 是一条可绑定候选。
type scopeCandidate struct {
	SourceNode string `json:"source_node"`
	SourcePort string `json:"source_port"`
	Type       string `json:"type"`
	TypeMatch  bool   `json:"type_match"`
}

// scopeCandidates 计算 targetID 的传递上游节点的所有输出端口，按类型过滤/标注。
// 连通性用 dsl.BuildDeps（edges ∪ ref bindings）的传递闭包；wantType 为空时不过滤，
// 但仍标注 type_match=false 供 Agent 参考。
func scopeCandidates(ir *dsl.IR, targetID, wantType string) []scopeCandidate {
	deps := dsl.BuildDeps(ir)
	upstream := transitiveUpstream(deps, targetID)

	out := make([]scopeCandidate, 0)
	for i := range ir.Nodes {
		n := &ir.Nodes[i]
		if !upstream[n.ID] {
			continue
		}
		for _, p := range n.Outputs {
			match := wantType == "" || p.Type == "" || p.Type == wantType
			// 类型已知且不匹配时，直接过滤掉（scope 的收敛价值）。
			if wantType != "" && p.Type != "" && p.Type != wantType {
				continue
			}
			out = append(out, scopeCandidate{
				SourceNode: n.ID,
				SourcePort: p.Name,
				Type:       p.Type,
				TypeMatch:  match,
			})
		}
	}
	return out
}

// transitiveUpstream 从 deps（node -> 直接前驱）求 targetID 的全部传递上游。
func transitiveUpstream(deps map[string][]string, targetID string) map[string]bool {
	seen := map[string]bool{}
	var visit func(id string)
	visit = func(id string) {
		for _, pre := range deps[id] {
			if !seen[pre] {
				seen[pre] = true
				visit(pre)
			}
		}
	}
	visit(targetID)
	return seen
}

// findPort 在端口列表里按名字找端口。
func findPort(ports []dsl.Port, name string) *dsl.Port {
	for i := range ports {
		if ports[i].Name == name {
			return &ports[i]
		}
	}
	return nil
}
