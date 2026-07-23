package scheduler

import (
	"context"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/smart-workflow/smart-workflow/internal/dsl"
	"github.com/smart-workflow/smart-workflow/internal/engine/nodes"
)

// countingExecutor 记录被调用次数与每次收到的迭代变量值，用于验证 batch 逐项执行。
type countingExecutor struct {
	calls    int32
	itemName string
	seen     []any
}

func (e *countingExecutor) Type() string { return "counting" }

func (e *countingExecutor) Execute(_ context.Context, ec *nodes.ExecContext) (*nodes.NodeResult, error) {
	atomic.AddInt32(&e.calls, 1)
	v := ec.Inputs[e.itemName]
	e.seen = append(e.seen, v)
	return &nodes.NodeResult{Outputs: map[string]any{"echo": v}}, nil
}

// batchNode 构造一个开启 batch 的节点，迭代变量为 item。
func batchNode(itemName string, size int) dsl.DSLNode {
	batch := map[string]any{"enable": true, "source_port": itemName}
	if size > 0 {
		batch["size"] = size
	}
	return dsl.DSLNode{
		ID: "app::batch",
		Data: dsl.NodeData{
			NodeMeta:    dsl.NodeMeta{NodeType: "counting"},
			NodeParam:   map[string]any{"batch": batch},
			RetryConfig: dsl.DefaultRetryConfig(),
		},
	}
}

// TestRunNode_BatchIteratesAndAggregates 验证 batch 逐项执行并聚合为 {items,count}。
func TestRunNode_BatchIteratesAndAggregates(t *testing.T) {
	exec := &countingExecutor{itemName: "rows"}
	c := &coord{}
	rows := []any{
		map[string]any{"q": "a"},
		map[string]any{"q": "b"},
		map[string]any{"q": "c"},
	}
	ec := &nodes.ExecContext{
		Node:   batchNode("rows", 0),
		Inputs: map[string]any{"rows": rows},
	}
	res, _, _, err := c.runNode(context.Background(), exec, ec, dsl.DefaultRetryConfig())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if exec.calls != 3 {
		t.Fatalf("executor called %d times, want 3", exec.calls)
	}
	if res.Outputs["count"] != 3 {
		t.Fatalf("count = %v, want 3", res.Outputs["count"])
	}
	items, ok := res.Outputs["items"].([]any)
	if !ok || len(items) != 3 {
		t.Fatalf("items not []any len 3: %T %v", res.Outputs["items"], res.Outputs["items"])
	}
	// 每次迭代应收到单个 item（而非整个数组）。
	first, _ := items[0].(map[string]any)
	if first["echo"].(map[string]any)["q"] != "a" {
		t.Fatalf("first item echo mismatch: %+v", items[0])
	}
}

// TestRunNode_BatchSizeTruncates 验证 size>0 截断处理条数。
func TestRunNode_BatchSizeTruncates(t *testing.T) {
	exec := &countingExecutor{itemName: "rows"}
	c := &coord{}
	ec := &nodes.ExecContext{
		Node:   batchNode("rows", 2),
		Inputs: map[string]any{"rows": []any{1, 2, 3, 4, 5}},
	}
	res, _, _, err := c.runNode(context.Background(), exec, ec, dsl.DefaultRetryConfig())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if exec.calls != 2 {
		t.Fatalf("executor called %d times, want 2 (size truncation)", exec.calls)
	}
	if res.Outputs["count"] != 2 {
		t.Fatalf("count = %v, want 2", res.Outputs["count"])
	}
}

// TestRunNode_BatchEmptyArray 验证空数组：0 次调用，输出空 items。
func TestRunNode_BatchEmptyArray(t *testing.T) {
	exec := &countingExecutor{itemName: "rows"}
	c := &coord{}
	ec := &nodes.ExecContext{
		Node:   batchNode("rows", 0),
		Inputs: map[string]any{"rows": []any{}},
	}
	res, _, _, err := c.runNode(context.Background(), exec, ec, dsl.DefaultRetryConfig())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if exec.calls != 0 {
		t.Fatalf("executor called %d times, want 0", exec.calls)
	}
	if res.Outputs["count"] != 0 {
		t.Fatalf("count = %v, want 0", res.Outputs["count"])
	}
}

// TestRunNode_BatchSourceNotArray 验证迭代源非数组时报错。
func TestRunNode_BatchSourceNotArray(t *testing.T) {
	exec := &countingExecutor{itemName: "rows"}
	c := &coord{}
	ec := &nodes.ExecContext{
		Node:   batchNode("rows", 0),
		Inputs: map[string]any{"rows": "not an array"},
	}
	_, _, _, err := c.runNode(context.Background(), exec, ec, dsl.DefaultRetryConfig())
	if err == nil || !strings.Contains(err.Error(), "not an array") {
		t.Fatalf("expected not-an-array error, got: %v", err)
	}
}

// TestRunNode_BatchMissingSource 验证迭代源输入不存在时报错。
func TestRunNode_BatchMissingSource(t *testing.T) {
	exec := &countingExecutor{itemName: "rows"}
	c := &coord{}
	ec := &nodes.ExecContext{
		Node:   batchNode("rows", 0),
		Inputs: map[string]any{"other": []any{1}},
	}
	_, _, _, err := c.runNode(context.Background(), exec, ec, dsl.DefaultRetryConfig())
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("expected source-not-found error, got: %v", err)
	}
}

// TestRunNode_NoBatchPassthrough 验证未开启 batch 时行为等同 runWithRetry（单次执行，原样输出）。
func TestRunNode_NoBatchPassthrough(t *testing.T) {
	exec := &countingExecutor{itemName: "rows"}
	c := &coord{}
	plain := dsl.DSLNode{
		ID:   "app::plain",
		Data: dsl.NodeData{NodeMeta: dsl.NodeMeta{NodeType: "counting"}, RetryConfig: dsl.DefaultRetryConfig()},
	}
	ec := &nodes.ExecContext{Node: plain, Inputs: map[string]any{"rows": "x"}}
	res, _, _, err := c.runNode(context.Background(), exec, ec, dsl.DefaultRetryConfig())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if exec.calls != 1 {
		t.Fatalf("executor called %d times, want 1", exec.calls)
	}
	// 非 batch：直接返回执行器原始输出（无 items 包装）。
	if _, wrapped := res.Outputs["items"]; wrapped {
		t.Fatalf("non-batch output should not be wrapped in items: %+v", res.Outputs)
	}
	if res.Outputs["echo"] != "x" {
		t.Fatalf("passthrough echo = %v, want x", res.Outputs["echo"])
	}
}

// TestRunNode_BatchItemFailurePropagates 验证某项失败时整体报错并带条目索引。
func TestRunNode_BatchItemFailurePropagates(t *testing.T) {
	failAt2 := &failingExecutor{failIndex: 2, itemName: "rows"}
	c := &coord{}
	ec := &nodes.ExecContext{
		Node:   batchNode("rows", 0),
		Inputs: map[string]any{"rows": []any{"a", "b", "c"}},
	}
	_, _, _, err := c.runNode(context.Background(), failAt2, ec, noRetry())
	if err == nil || !strings.Contains(err.Error(), "item 2") {
		t.Fatalf("expected item 2 failure, got: %v", err)
	}
}

// failingExecutor 在第 failIndex 次调用（0-based）返回错误。
type failingExecutor struct {
	failIndex int
	itemName  string
	n         int
}

func (e *failingExecutor) Type() string { return "failing" }

func (e *failingExecutor) Execute(_ context.Context, ec *nodes.ExecContext) (*nodes.NodeResult, error) {
	idx := e.n
	e.n++
	if idx == e.failIndex {
		return nil, fmt.Errorf("boom at %d", idx)
	}
	return &nodes.NodeResult{Outputs: map[string]any{"echo": ec.Inputs[e.itemName]}}, nil
}

// noRetry 返回不重试的配置（batch item 失败即刻冒泡，不受重试放大）。
func noRetry() dsl.RetryConfig {
	c := dsl.DefaultRetryConfig()
	c.ShouldRetry = false
	c.MaxRetries = 0
	return c
}
