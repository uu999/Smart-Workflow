// Package runevent 定义工作流运行的对外事件契约（M9-b SSE 流式推送）。
//
// 这是一个零内部依赖的叶子包：engine/scheduler 只依赖它发事件，
// Redis / SSE / CLI 只依赖它消费事件——事件 schema 与 node_run 落库字段解耦，
// 是面向 --stream 与前端画布的稳定契约（对标 PaiFlow node start/process/end）。
package runevent

import "time"

// Phase 是事件阶段。
const (
	PhaseNodeStart = "node_start" // 节点开始执行
	PhaseNodeEnd   = "node_end"   // 节点执行结束（succeeded/failed/skipped）
	PhaseRunEnd    = "run_end"    // 整个 run 到终态，流结束信号（客户端据此断开）
)

// RunEvent 是一条运行事件。字段面向外部消费者稳定，不透出引擎内部表示
// （如 time.Duration、内部 status 常量），CostMs 用毫秒整数、Status 用字符串。
type RunEvent struct {
	RunID    string         `json:"run_id"`
	Seq      int64          `json:"seq"`                 // 单调递增序号，供有序/断点续传（Redis message id 另存）
	Phase    string         `json:"phase"`               // node_start / node_end / run_end
	NodeID   string         `json:"node_id,omitempty"`   // run_end 时为空
	NodeType string         `json:"node_type,omitempty"`
	Status   string         `json:"status,omitempty"`    // node_end: 节点状态；run_end: run 终态
	Output   map[string]any `json:"output,omitempty"`    // node_end 成功时的输出
	Error    string         `json:"error,omitempty"`
	CostMs   int64          `json:"cost_ms,omitempty"`
	TS       int64          `json:"ts"`                  // Unix 毫秒时间戳
}

// NowMillis 返回当前 Unix 毫秒（构造事件 TS 用）。
func NowMillis() int64 { return time.Now().UnixMilli() }

// Emitter 是事件发射器抽象。engine/scheduler 只依赖它，默认 Nop（零开销）；
// 生产按模式注入内存 Hub 或 Redis Stream 实现，engine 不感知 Redis。
type Emitter interface {
	Emit(evt RunEvent)
}

// NopEmitter 是空实现：不配 SSE 时使用，engine 行为与之前完全一致。
type NopEmitter struct{}

func (NopEmitter) Emit(RunEvent) {}
