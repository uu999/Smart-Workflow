package cli

import (
	"github.com/spf13/cobra"

	"github.com/smart-workflow/smart-workflow/internal/dsl"
)

// newCloneRefCmd: swf clone-ref --workflow-id <id> [--name] [--project-id] [--sid]
// 复用优先（对齐蓝图/Byteval）：拉服务端已发布工作流的 DSL → dsl.ToIR 反渲染成
// 本地 IR 会话 → 落盘，供后续 add-node/bind 二次修改。Meta.Source 记来源工作流。
//
// 与 `init --from-dsl` 同一反渲染路径，差别仅在 DSL 来源：clone-ref 从服务端按
// workflow-id 拉，init --from-dsl 读本地文件。
func newCloneRefCmd(a *appCtx) *cobra.Command {
	var workflowID, name, projectID, sid string
	cmd := &cobra.Command{
		Use:   "clone-ref",
		Short: "克隆已有工作流到本地 IR 会话（复用优先，二次修改）",
		RunE: func(_ *cobra.Command, _ []string) error {
			if workflowID == "" {
				return a.emitErr(newErr("BAD_REQUEST", "--workflow-id is required", "swf search --kind workflow 找可复用工作流"))
			}

			// 拉服务端工作流：draft 是完整编辑态 DSL，name 供缺省会话名。
			var wf struct {
				WorkflowID string   `json:"workflow_id"`
				Name       string   `json:"name"`
				ProjectID  string   `json:"project_id"`
				Draft      *dsl.DSL `json:"draft"`
			}
			if err := a.client().doJSON("GET", "/v1/workflows/"+workflowID, nil, &wf); err != nil {
				return a.emitErr(err)
			}
			if wf.Draft == nil || len(wf.Draft.Nodes) == 0 {
				return a.emitErr(newErr("EMPTY_WORKFLOW",
					"source workflow has no draft graph to clone", "确认该工作流已建图（有节点）"))
			}

			// 缺省沿用来源名/项目，允许显式覆盖。
			if name == "" {
				name = wf.Name
			}
			if projectID == "" {
				projectID = wf.ProjectID
			}
			if sid == "" {
				sid = genSID()
			}

			s, err := NewSession(sid, Meta{Name: name, ProjectID: projectID})
			if err != nil {
				return a.emitErr(newErr("SESSION_EXISTS", err.Error(), "换一个 --sid 或删除旧会话"))
			}

			ir, terr := dsl.ToIR(wf.Draft, dsl.Meta{Name: name, ProjectID: projectID, Source: workflowID})
			if terr != nil {
				return a.emitErr(newErr("CLONE_FAILED", terr.Error(), ""))
			}
			s.IR = ir
			s.Meta.Source = workflowID
			if serr := s.Save(); serr != nil {
				return a.emitErr(serr)
			}

			return a.emitOK(map[string]any{
				"sid":         s.ID,
				"dir":         s.Dir(),
				"name":        name,
				"source":      workflowID,
				"node_num":    len(ir.Nodes),
				"edge_num":    len(ir.Edges),
			})
		},
	}
	cmd.Flags().StringVar(&workflowID, "workflow-id", "", "要克隆的来源工作流 ID（必填）")
	cmd.Flags().StringVar(&name, "name", "", "新会话名称（缺省沿用来源工作流名）")
	cmd.Flags().StringVar(&projectID, "project-id", "", "项目 ID（缺省沿用来源）")
	cmd.Flags().StringVar(&sid, "sid", "", "指定会话 ID（缺省自动生成）")
	return cmd
}
