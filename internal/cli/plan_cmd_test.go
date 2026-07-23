package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/smart-workflow/smart-workflow/internal/dsl"
)

// writePlan 把 plan 对象写成临时 plan.json，返回路径。
func writePlan(t *testing.T, plan map[string]any) string {
	t.Helper()
	b, _ := json.MarshalIndent(plan, "", "  ")
	path := filepath.Join(t.TempDir(), "plan.json")
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatalf("write plan: %v", err)
	}
	return path
}

// evalPlan 构造一份「dataset→分类器(application)→end」的声明式建图 plan，含内联 bindings。
func evalPlan() map[string]any {
	return map[string]any{
		"workflow": map[string]any{"name": "情感评测", "project_id": "6970"},
		"nodes": []any{
			map[string]any{"id": "dataset_0", "kind": "dataset", "dataset_id": "ds_1",
				"outputs": []any{map[string]any{"name": "rows", "type": "array"}}},
			map[string]any{"id": "clf_0", "kind": "application", "app_id": "app_1", "title": "情感分类器",
				"inputs": []any{map[string]any{"name": "text", "type": "string", "required": true}},
				"bindings": []any{
					map[string]any{"port": "text", "source_node": "dataset_0", "source_port": "rows"}, // mode 缺省推 ref
				}},
			map[string]any{"id": "end_0", "kind": "end",
				"bindings": []any{
					map[string]any{"port": "label", "mode": "ref", "source_node": "clf_0", "source_port": "label"},
				}},
		},
		"edges": []any{
			map[string]any{"source": "dataset_0", "target": "clf_0"},
			map[string]any{"source": "clf_0", "target": "end_0"},
		},
	}
}

// TestCLI_PlanApply_BuildsGraph 验证 plan-apply 从一份 JSON 一次性建出图：
// 节点/边/绑定数正确，mode 缺省推断为 ref，且会话可被后续命令加载。
func TestCLI_PlanApply_BuildsGraph(t *testing.T) {
	t.Setenv("SWF_SESSIONS_DIR", t.TempDir())
	path := writePlan(t, evalPlan())

	env, err := runCLI(t, "plan-apply", "--file", path)
	if err != nil || !env.OK {
		t.Fatalf("plan-apply failed: err=%v env=%+v", err, env)
	}
	if n, _ := dataField(t, env, "node_num").(float64); n != 3 {
		t.Fatalf("node_num = %v, want 3", dataField(t, env, "node_num"))
	}
	if e, _ := dataField(t, env, "edge_num").(float64); e != 2 {
		t.Fatalf("edge_num = %v, want 2", dataField(t, env, "edge_num"))
	}

	// 会话应可被 preview 加载（证明落盘的 IR 结构合法）。
	sid := dataField(t, env, "sid").(string)
	if _, verr := runCLI(t, "preview", "--sid", sid); verr != nil {
		t.Fatalf("plan-applied session not loadable: %v", verr)
	}
}

// TestCLI_PlanApply_AppliesToExistingSession 验证 --sid 时把 plan 追加到既有会话。
func TestCLI_PlanApply_AppliesToExistingSession(t *testing.T) {
	t.Setenv("SWF_SESSIONS_DIR", t.TempDir())
	// 先建一个只有 start 的会话。
	env, _ := runCLI(t, "init", "--name", "base")
	sid := dataField(t, env, "sid").(string)
	if _, err := runCLI(t, "add-node", "--sid", sid, "--id", "start_0", "--kind", "start"); err != nil {
		t.Fatalf("add-node: %v", err)
	}

	// plan 只加一个 end + 一条边，应叠加到既有 start 上。
	plan := map[string]any{
		"nodes": []any{map[string]any{"id": "end_0", "kind": "end"}},
		"edges": []any{map[string]any{"source": "start_0", "target": "end_0"}},
	}
	path := writePlan(t, plan)
	env2, err := runCLI(t, "plan-apply", "--sid", sid, "--file", path)
	if err != nil || !env2.OK {
		t.Fatalf("plan-apply to existing failed: err=%v env=%+v", err, env2)
	}
	if n, _ := dataField(t, env2, "node_num").(float64); n != 2 {
		t.Fatalf("node_num = %v, want 2 (start+end)", dataField(t, env2, "node_num"))
	}
}

// TestCLI_PlanApply_DuplicateNode 验证 plan 内节点 id 与既有会话冲突 → NODE_EXISTS。
func TestCLI_PlanApply_DuplicateNode(t *testing.T) {
	t.Setenv("SWF_SESSIONS_DIR", t.TempDir())
	env, _ := runCLI(t, "init", "--name", "base")
	sid := dataField(t, env, "sid").(string)
	_, _ = runCLI(t, "add-node", "--sid", sid, "--id", "start_0", "--kind", "start")

	plan := map[string]any{"nodes": []any{map[string]any{"id": "start_0", "kind": "start"}}}
	path := writePlan(t, plan)
	env2, err := runCLI(t, "plan-apply", "--sid", sid, "--file", path)
	if err == nil {
		t.Fatal("expected NODE_EXISTS for duplicate id")
	}
	if env2.OK || env2.Error == nil || env2.Error.Code != "NODE_EXISTS" {
		t.Fatalf("expected NODE_EXISTS, got %+v", env2)
	}
}

// TestCLI_PlanApply_EdgeUnknownNode 验证边引用不存在节点 → NODE_NOT_FOUND。
func TestCLI_PlanApply_EdgeUnknownNode(t *testing.T) {
	t.Setenv("SWF_SESSIONS_DIR", t.TempDir())
	plan := map[string]any{
		"workflow": map[string]any{"name": "x"},
		"nodes":    []any{map[string]any{"id": "start_0", "kind": "start"}},
		"edges":    []any{map[string]any{"source": "start_0", "target": "ghost"}},
	}
	path := writePlan(t, plan)
	env, err := runCLI(t, "plan-apply", "--file", path)
	if err == nil {
		t.Fatal("expected NODE_NOT_FOUND for dangling edge")
	}
	if env.OK || env.Error == nil || env.Error.Code != "NODE_NOT_FOUND" {
		t.Fatalf("expected NODE_NOT_FOUND, got %+v", env)
	}
}

// TestCLI_PlanApply_MissingFile 验证缺 --file → BAD_REQUEST。
func TestCLI_PlanApply_MissingFile(t *testing.T) {
	env, err := runCLI(t, "plan-apply")
	if err == nil {
		t.Fatal("expected error for missing --file")
	}
	if env.OK || env.Error == nil || env.Error.Code != "BAD_REQUEST" {
		t.Fatalf("expected BAD_REQUEST, got %+v", env)
	}
}

// TestCLI_PlanSchema_Prints 验证 plan-schema 打印规格（含 nodes[]/edges[] 键）。
func TestCLI_PlanSchema_Prints(t *testing.T) {
	env, err := runCLI(t, "plan-schema")
	if err != nil || !env.OK {
		t.Fatalf("plan-schema failed: err=%v env=%+v", err, env)
	}
	m, ok := env.Data.(map[string]any)
	if !ok || m["nodes[]"] == nil || m["edges[]"] == nil {
		t.Fatalf("plan-schema output missing keys: %+v", env.Data)
	}
}

// TestCLI_PlanApply_BatchAndParams 验证 plan-apply 能建出带 batch(分类器逐条跑)
// 与 params(code 节点代码)的可执行图：应用后加载会话，断言 IR 的 Batch/Params 落地，
// 且渲染出的 DSL nodeParam 含 batch + code（证明 render 透传，执行期可被 scheduler/CodeExecutor 读取）。
func TestCLI_PlanApply_BatchAndParams(t *testing.T) {
	t.Setenv("SWF_SESSIONS_DIR", t.TempDir())
	plan := map[string]any{
		"workflow": map[string]any{"name": "批量分类", "project_id": "6970"},
		"nodes": []any{
			map[string]any{"id": "ds_0", "kind": "dataset", "dataset_id": "ds_1",
				"outputs": []any{map[string]any{"name": "rows", "type": "array"}}},
			// 分类器：对 dataset 的 rows 逐条跑（batch），迭代变量名 item。
			map[string]any{"id": "clf_0", "kind": "application", "app_id": "app_1",
				"inputs": []any{map[string]any{"name": "item", "type": "object", "required": true}},
				"batch": map[string]any{
					"enable": true, "source_node": "ds_0", "source_port": "rows", "item_name": "item",
				},
				"bindings": []any{
					map[string]any{"port": "item", "source_node": "ds_0", "source_port": "rows"},
				}},
			// 结果合并 code 节点：params.code 存 python。
			map[string]any{"id": "merge_0", "kind": "code",
				"params": map[string]any{"code": "sink({'n': len(inputs.get('items', []))})"},
				"inputs": []any{map[string]any{"name": "items", "type": "array"}},
				"bindings": []any{
					map[string]any{"port": "items", "source_node": "clf_0", "source_port": "items"},
				}},
			map[string]any{"id": "end_0", "kind": "end"},
		},
		"edges": []any{
			map[string]any{"source": "ds_0", "target": "clf_0"},
			map[string]any{"source": "clf_0", "target": "merge_0"},
			map[string]any{"source": "merge_0", "target": "end_0"},
		},
	}
	path := writePlan(t, plan)

	env, err := runCLI(t, "plan-apply", "--file", path)
	if err != nil || !env.OK {
		t.Fatalf("plan-apply failed: err=%v env=%+v", err, env)
	}
	sid := dataField(t, env, "sid").(string)

	// 加载会话，断言 IR 携带 batch 与 params。
	s, lerr := LoadSession(sid)
	if lerr != nil {
		t.Fatalf("load session: %v", lerr)
	}
	var clf, merge *dsl.Node
	for i := range s.IR.Nodes {
		switch s.IR.Nodes[i].ID {
		case "clf_0":
			clf = &s.IR.Nodes[i]
		case "merge_0":
			merge = &s.IR.Nodes[i]
		}
	}
	if clf == nil || clf.Batch == nil || !clf.Batch.Enable || clf.Batch.ItemName != "item" || clf.Batch.SourcePort != "rows" {
		t.Fatalf("clf batch not applied: %+v", clf)
	}
	if merge == nil || merge.Params["code"] == "" {
		t.Fatalf("merge params.code not applied: %+v", merge)
	}

	// 渲染成 DSL，断言 nodeParam 透传 batch 与 code（执行期真正读的地方）。
	rendered, rerr := renderSession(s)
	if rerr != nil {
		t.Fatalf("render: %v", rerr)
	}
	var sawBatch, sawCode bool
	for _, dn := range rendered.Nodes {
		if _, ok := dn.Data.NodeParam["batch"]; ok && dn.Data.NodeMeta.NodeType == "application" {
			sawBatch = true
		}
		if code, ok := dn.Data.NodeParam["code"].(string); ok && code != "" {
			sawCode = true
		}
	}
	if !sawBatch {
		t.Fatal("rendered DSL application node missing nodeParam.batch")
	}
	if !sawCode {
		t.Fatal("rendered DSL code node missing nodeParam.code")
	}
}
