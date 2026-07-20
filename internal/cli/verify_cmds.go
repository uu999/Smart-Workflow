package cli

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/smart-workflow/smart-workflow/internal/dsl"
	"github.com/smart-workflow/smart-workflow/internal/validator"
)

// newValidateCmd: swf validate --sid
// 进程内跑：IR → validator.Validate（不触服务端、零成本、秒级）。
// 这是"upload 前修到 0 error"闭环的第一层（风险1 决策）。
func newValidateCmd(a *appCtx) *cobra.Command {
	var sid string
	cmd := &cobra.Command{
		Use:   "validate",
		Short: "静态校验（进程内，30+ 类检查，必须 0 error）",
		RunE: func(_ *cobra.Command, _ []string) error {
			s, err := mustLoad(a, sid)
			if err != nil {
				return err
			}
			res := validator.Validate(s.IR)
			errs, warns := countSeverity(res.Issues)
			return a.emitOK(map[string]any{
				"sid":       s.ID,
				"has_error": res.HasError(),
				"error_num": errs,
				"warn_num":  warns,
				"issues":    res.Issues,
			})
		},
	}
	cmd.Flags().StringVar(&sid, "sid", "", "会话 ID（必填）")
	return cmd
}

// newPreviewCmd: swf preview --sid [--print-full]
// 进程内把 IR 渲染成 DSL 并落 dsl.json；默认打印摘要，--print-full 打印完整 DSL。
func newPreviewCmd(a *appCtx) *cobra.Command {
	var sid string
	var printFull bool
	cmd := &cobra.Command{
		Use:   "preview",
		Short: "渲染 IR→DSL 并落 dsl.json，打印摘要或完整 DSL",
		RunE: func(_ *cobra.Command, _ []string) error {
			s, err := mustLoad(a, sid)
			if err != nil {
				return err
			}
			d, rerr := renderSession(s)
			if rerr != nil {
				return a.emitErr(newErr("RENDER_FAILED", rerr.Error(), "先 swf validate 定位问题"))
			}
			if serr := s.SaveDSL(d); serr != nil {
				return a.emitErr(serr)
			}
			data := map[string]any{
				"sid":       s.ID,
				"node_num":  len(d.Nodes),
				"edge_num":  len(d.Edges),
				"dsl_path":  s.Dir() + "/" + fileDSL,
			}
			if printFull {
				data["dsl"] = d
			} else {
				data["nodes"] = summarizeNodes(d)
			}
			return a.emitOK(data)
		},
	}
	cmd.Flags().StringVar(&sid, "sid", "", "会话 ID（必填）")
	cmd.Flags().BoolVar(&printFull, "print-full", false, "打印完整 DSL")
	return cmd
}

// newNodeDebugCmd: swf node-debug --sid --node-id [--inputs json] [--cost-target-sec n]
// 进程内渲染出 DSL，取出目标节点，转发服务端无状态 /v1/node-debug（复用引擎+sidecar）。
func newNodeDebugCmd(a *appCtx) *cobra.Command {
	var sid, nodeID, inputsJSON string
	var costTarget float64
	cmd := &cobra.Command{
		Use:   "node-debug",
		Short: "单节点隔离调试（服务端真跑，关重试 + 断言）",
		RunE: func(_ *cobra.Command, _ []string) error {
			s, err := mustLoad(a, sid)
			if err != nil {
				return err
			}
			if nodeID == "" {
				return a.emitErr(newErr("BAD_REQUEST", "--node-id is required", ""))
			}
			d, rerr := renderSession(s)
			if rerr != nil {
				return a.emitErr(newErr("RENDER_FAILED", rerr.Error(), "先 swf validate"))
			}
			// 渲染后 IR 可读 ID 已变真实 ID：用 nodeMeta.aliasName / IR 顺序对齐找目标。
			target, ok := findRenderedNode(s.IR, d, nodeID)
			if !ok {
				return a.emitErr(newErr("NODE_NOT_FOUND", fmt.Sprintf("node %q not found in session", nodeID), "swf preview 看现有节点"))
			}
			var inputs map[string]any
			if inputsJSON != "" {
				if json.Unmarshal([]byte(inputsJSON), &inputs) != nil {
					return a.emitErr(newErr("INVALID_JSON", "--inputs is not valid JSON object", `如 --inputs '{"text":"hi"}'`))
				}
			}
			var result any
			derr := a.client().doJSON("POST", "/v1/node-debug", map[string]any{
				"node":            target,
				"inputs":          inputs,
				"cost_target_sec": costTarget,
			}, &result)
			if derr != nil {
				return a.emitErr(derr)
			}
			return a.emitOK(result)
		},
	}
	cmd.Flags().StringVar(&sid, "sid", "", "会话 ID（必填）")
	cmd.Flags().StringVar(&nodeID, "node-id", "", "要调试的可读节点 ID（必填）")
	cmd.Flags().StringVar(&inputsJSON, "inputs", "", `节点输入 JSON，如 '{"text":"hi"}'`)
	cmd.Flags().Float64Var(&costTarget, "cost-target-sec", 0, "耗时断言目标秒数（>0 生效）")
	return cmd
}

// newRunCmd: swf run --workflow-id --input [--version n] [--wait]
// 转发服务端 POST /v1/runs（异步），--wait 时轮询到终态。
func newRunCmd(a *appCtx) *cobra.Command {
	var workflowID, inputJSON string
	var version int
	var versionSet, wait bool
	cmd := &cobra.Command{
		Use:   "run",
		Short: "整图端到端运行（服务端异步，--wait 轮询终态）",
		RunE: func(c *cobra.Command, _ []string) error {
			if workflowID == "" {
				return a.emitErr(newErr("BAD_REQUEST", "--workflow-id is required", "先 swf upload 拿到 workflow_id"))
			}
			body := map[string]any{"workflow_id": workflowID}
			if inputJSON != "" {
				var in any
				if json.Unmarshal([]byte(inputJSON), &in) != nil {
					return a.emitErr(newErr("INVALID_JSON", "--input is not valid JSON", ""))
				}
				body["input"] = in
			}
			if c.Flags().Changed("version") {
				versionSet = true
			}
			if versionSet {
				body["version"] = version
			}
			var runResp struct {
				RunID  string `json:"run_id"`
				Status string `json:"status"`
			}
			if rerr := a.client().doJSON("POST", "/v1/runs", body, &runResp); rerr != nil {
				return a.emitErr(rerr)
			}
			if !wait {
				return a.emitOK(runResp)
			}
			final, perr := a.pollRun(runResp.RunID)
			if perr != nil {
				return a.emitErr(perr)
			}
			return a.emitOK(final)
		},
	}
	cmd.Flags().StringVar(&workflowID, "workflow-id", "", "已上传的工作流 ID（必填）")
	cmd.Flags().StringVar(&inputJSON, "input", "", `运行入参 JSON，如 '{"query":"hi"}'`)
	cmd.Flags().IntVar(&version, "version", 0, "版本：省略=最新发布 / -1=草稿 / N>0=指定")
	cmd.Flags().BoolVar(&wait, "wait", false, "轮询直到运行终态")
	return cmd
}

// newUploadCmd: swf upload --sid [--description] [--update-id id]
// 渲染 IR→DSL，创建新工作流草稿（默认新副本）；--update-id 覆盖更新（需 --confirm）。
func newUploadCmd(a *appCtx) *cobra.Command {
	var sid, description, updateID string
	var confirm bool
	var versionLock int
	cmd := &cobra.Command{
		Use:   "upload",
		Short: "上传草稿（默认新副本；--update-id 覆盖更新需 --confirm）",
		RunE: func(_ *cobra.Command, _ []string) error {
			s, err := mustLoad(a, sid)
			if err != nil {
				return err
			}
			d, rerr := renderSession(s)
			if rerr != nil {
				return a.emitErr(newErr("RENDER_FAILED", rerr.Error(), "先 swf validate"))
			}
			_ = s.SaveDSL(d)

			if updateID != "" {
				if !confirm {
					return a.emitErr(newErr("CONFIRM_REQUIRED",
						fmt.Sprintf("overwriting workflow %q needs --confirm", updateID),
						"确认覆盖请加 --confirm；否则去掉 --update-id 走新副本"))
				}
				body := map[string]any{"name": s.Meta.Name, "description": description, "draft": d, "version_lock": versionLock}
				var resp map[string]any
				if uerr := a.client().doJSON("PUT", "/v1/workflows/"+updateID, body, &resp); uerr != nil {
					return a.emitErr(uerr)
				}
				return a.emitOK(map[string]any{"sid": s.ID, "workflow_id": updateID, "updated": true})
			}

			body := map[string]any{"project_id": s.Meta.ProjectID, "name": s.Meta.Name, "description": description, "draft": d}
			var resp struct {
				WorkflowID string `json:"workflow_id"`
			}
			if uerr := a.client().doJSON("POST", "/v1/workflows", body, &resp); uerr != nil {
				return a.emitErr(uerr)
			}
			// 回填 source 便于后续迭代。
			s.Meta.Source = resp.WorkflowID
			_ = s.Save()
			return a.emitOK(map[string]any{"sid": s.ID, "workflow_id": resp.WorkflowID, "created": true})
		},
	}
	cmd.Flags().StringVar(&sid, "sid", "", "会话 ID（必填）")
	cmd.Flags().StringVar(&description, "description", "", "工作流描述")
	cmd.Flags().StringVar(&updateID, "update-id", "", "覆盖更新的目标工作流 ID")
	cmd.Flags().BoolVar(&confirm, "confirm", false, "确认覆盖更新")
	cmd.Flags().IntVar(&versionLock, "version-lock", 0, "覆盖更新时的乐观锁值")
	return cmd
}

// --- 内部工具 ---

func countSeverity(issues []validator.Issue) (errs, warns int) {
	for _, i := range issues {
		if i.Severity == validator.SeverityError {
			errs++
		} else {
			warns++
		}
	}
	return
}

// summarizeNodes 生成节点摘要（id/type/alias），preview 默认视图。
func summarizeNodes(d *dsl.DSL) []map[string]any {
	out := make([]map[string]any, 0, len(d.Nodes))
	for _, n := range d.Nodes {
		out = append(out, map[string]any{
			"id":    n.ID,
			"type":  n.Data.NodeMeta.NodeType,
			"alias": n.Data.NodeMeta.AliasName,
		})
	}
	return out
}

// findRenderedNode 用 IR 里可读 ID 的位置，定位渲染后 DSL 的对应节点。
// Render 保持节点顺序，故按 IR.Nodes 的下标对齐 d.Nodes。
func findRenderedNode(ir *dsl.IR, d *dsl.DSL, readableID string) (dsl.DSLNode, bool) {
	idx := -1
	for i, n := range ir.Nodes {
		if n.ID == readableID {
			idx = i
			break
		}
	}
	if idx < 0 || idx >= len(d.Nodes) {
		return dsl.DSLNode{}, false
	}
	return d.Nodes[idx], true
}
