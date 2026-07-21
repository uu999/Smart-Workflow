package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/smart-workflow/smart-workflow/internal/httpx"
	"github.com/smart-workflow/smart-workflow/internal/runevent"
	"github.com/smart-workflow/smart-workflow/internal/service"
)

// sseHeartbeat 是无事件时的心跳间隔，兼作「订阅后 run 已终态」的兜底探测周期：
// 客户端在此期间没有新事件时收到一条注释帧保活，同时服务端复查 DB 终态以收敛流。
const sseHeartbeat = 15 * time.Second

// streamRun 是 SSE 端点 GET /v1/runs/:id/events：把该 run 的运行事件按
// Server-Sent Events 推给客户端（swf run --stream / 前端画布消费）。
//
// 事件来源是注入的 eventbus.Source（dispatcher 模式=共享 MemHub；asynq 模式=RedisSource），
// engine 侧只管发事件、不感知这里。未配置 Source 时回 501，CLI 据此回退轮询
// GET /runs/:id（优雅降级）。
//
// 收敛保证：
//   - 正常：收到 run_end → 发出后结束流。
//   - 迟到订阅（run 已终态、内存事件已消失/流已过期）：GET /runs/:id 若已终态，
//     直接把节点快照回放成 node_end + 合成 run_end，避免流永久挂起。
//   - 客户端断开：c.Request.Context() 取消 → Source 退订、goroutine 收敛。
func (h *handlers) streamRun(c *gin.Context) {
	runID := c.Param("id")

	if h.events == nil {
		// 无事件源（如进程内 dispatcher 未装配 MemHub）：明确告知不支持，
		// CLI 收到 501 回退轮询，而不是让用户对着空流干等。
		httpx.Fail(c, http.StatusNotImplemented, "STREAM_UNSUPPORTED",
			"event streaming is not enabled on this server", gin.H{
				"hint": "poll instead: GET /v1/runs/" + runID,
			})
		return
	}

	// run 必须存在（顺带拿当前状态，用于迟到订阅的快速收敛）。
	view, err := h.runs.GetRun(c.Request.Context(), runID)
	if err != nil {
		failFromErr(c, err)
		return
	}

	setSSEHeaders(c)
	ctx := c.Request.Context()

	// 迟到订阅兜底：run 已终态 → 直接回放快照，不进订阅（内存事件可能早已消失）。
	if isTerminalStatus(view.Status) {
		replayTerminal(c, view)
		return
	}

	ch, err := h.events.Subscribe(ctx, runID)
	if err != nil {
		h.logger.Warn("sse subscribe failed", zap.String("run_id", runID), zap.Error(err))
		writeSSEError(c, "SUBSCRIBE_FAILED", err.Error())
		return
	}

	h.logger.Info("sse stream open", zap.String("run_id", runID))
	ticker := time.NewTicker(sseHeartbeat)
	defer ticker.Stop()

	c.Stream(func(_ io.Writer) bool {
		select {
		case <-ctx.Done():
			return false // 客户端断开：停止流（Subscribe 内部随 ctx 退订）
		case evt, ok := <-ch:
			if !ok {
				return false // Source 关闭（run_end 或退订）：流自然结束
			}
			writeSSEEvent(c, evt)
			return evt.Phase != runevent.PhaseRunEnd
		case <-ticker.C:
			// 心跳保活；顺带兜底：若订阅后 run 恰好已终态而事件漏拍，复查 DB 收敛。
			if v, terminal := h.checkTerminal(ctx, runID); terminal {
				replayTerminal(c, v)
				return false
			}
			writeSSEComment(c, "keepalive")
			return true
		}
	})
	h.logger.Info("sse stream closed", zap.String("run_id", runID))
}

// checkTerminal 复查 run 是否已终态（心跳兜底用）。读失败时保守返回 false（继续等事件）。
func (h *handlers) checkTerminal(ctx context.Context, runID string) (*service.RunView, bool) {
	view, err := h.runs.GetRun(ctx, runID)
	if err != nil {
		return nil, false
	}
	return view, isTerminalStatus(view.Status)
}

// replayTerminal 把已终态 run 的节点快照回放为 node_end 事件，末尾补一条 run_end。
// 供「订阅时 run 已结束」的迟到客户端一次性拿到全貌并收敛（内存事件已不可得时的权威回退）。
func replayTerminal(c *gin.Context, view *service.RunView) {
	var seq int64
	for _, n := range view.Nodes {
		seq++
		writeSSEEvent(c, runevent.RunEvent{
			RunID:    view.RunID,
			Seq:      seq,
			Phase:    runevent.PhaseNodeEnd,
			NodeID:   n.NodeID,
			NodeType: n.NodeType,
			Status:   n.Status,
			Output:   rawToMap(n.Output),
			Error:    n.Error,
			CostMs:   int64(n.CostMs),
			TS:       runevent.NowMillis(),
		})
	}
	seq++
	writeSSEEvent(c, runevent.RunEvent{
		RunID:  view.RunID,
		Seq:    seq,
		Phase:  runevent.PhaseRunEnd,
		Status: view.Status,
		Error:  view.Error,
		TS:     runevent.NowMillis(),
	})
}

// rawToMap 把 node_run 落库的 output(JSON) 尽力解成 map；失败或空返回 nil（省略字段）。
func rawToMap(raw json.RawMessage) map[string]any {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil
	}
	return m
}

// setSSEHeaders 写 SSE 必需响应头（禁缓存、保持连接、关闭代理缓冲）。
func setSSEHeaders(c *gin.Context) {
	c.Writer.Header().Set("Content-Type", "text/event-stream")
	c.Writer.Header().Set("Cache-Control", "no-cache")
	c.Writer.Header().Set("Connection", "keep-alive")
	c.Writer.Header().Set("X-Accel-Buffering", "no") // 禁用 nginx 缓冲，保证实时
}

// writeSSEEvent 按 SSE 帧写一条事件：id + event + data(JSON) + 空行。
// id 用 seq，便于将来 Last-Event-ID 断点续传（本阶段客户端未使用）。
func writeSSEEvent(c *gin.Context, evt runevent.RunEvent) {
	b, err := json.Marshal(evt)
	if err != nil {
		return
	}
	if evt.Seq > 0 {
		fmt.Fprintf(c.Writer, "id: %d\n", evt.Seq)
	}
	fmt.Fprintf(c.Writer, "event: %s\ndata: %s\n\n", evt.Phase, b)
	c.Writer.Flush()
}

// writeSSEComment 写一条 SSE 注释帧（以 ':' 开头）用于心跳保活，不触发客户端事件回调。
func writeSSEComment(c *gin.Context, text string) {
	fmt.Fprintf(c.Writer, ": %s\n\n", text)
	c.Writer.Flush()
}

// writeSSEError 以 SSE data 帧下发错误（流已 200 开启，无法再改 HTTP 状态码）。
func writeSSEError(c *gin.Context, code, message string) {
	payload := map[string]any{"error": map[string]string{"code": code, "message": message}}
	b, _ := json.Marshal(payload)
	fmt.Fprintf(c.Writer, "event: error\ndata: %s\n\n", b)
	c.Writer.Flush()
}

// isTerminalStatus 判断 run 状态是否为终态（对齐 engine.RunStatus*）。
func isTerminalStatus(s string) bool {
	switch s {
	case "succeeded", "failed", "canceled":
		return true
	}
	return false
}
