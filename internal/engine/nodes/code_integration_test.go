//go:build integration

package nodes

import (
	"context"
	"os"
	"testing"

	"github.com/smart-workflow/smart-workflow/internal/dsl"
)

// 运行方式（需先启动 sidecar）：
//
//	sidecar/.venv/bin/python -m uvicorn main:app --host 127.0.0.1 --port 8090 --app-dir sidecar &
//	SWF_SIDECAR_BASEURL=http://127.0.0.1:8090 go test -tags integration ./internal/engine/nodes -run TestM5 -v
//
// 验证 Go code 节点 → 真实 Python runner 的完整链路（子进程隔离执行 + sink 回传）。
func TestM5_CodeNodeRealSidecar(t *testing.T) {
	base := os.Getenv("SWF_SIDECAR_BASEURL")
	if base == "" {
		base = "http://127.0.0.1:8090"
	}

	exec := CodeExecutor{SidecarURL: base}
	node := dsl.DSLNode{
		ID: "code::real",
		Data: dsl.NodeData{
			NodeMeta: dsl.NodeMeta{NodeType: dsl.KindCode},
			NodeParam: map[string]any{
				"code":    "total = sum(inputs['nums'])\nprint('computed', total)\nsink({'total': total, 'n': len(inputs['nums'])})",
				"timeout": float64(10),
			},
		},
	}

	res, err := exec.Execute(context.Background(), &ExecContext{
		Node:   node,
		Inputs: map[string]any{"nums": []any{1, 2, 3, 4}},
	})
	if err != nil {
		t.Fatalf("code node execute: %v", err)
	}
	if res.Outputs["total"] != 10.0 {
		t.Fatalf("total = %v, want 10", res.Outputs["total"])
	}
	if res.Outputs["n"] != 4.0 {
		t.Fatalf("n = %v, want 4", res.Outputs["n"])
	}
}
