package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/smart-workflow/smart-workflow/internal/runevent"
)

// streamRun 订阅服务端 SSE 事件流（GET /v1/runs/{id}/events），把每步事件以
// 紧凑 NDJSON envelope 逐行打印，直到收到 run_end；随后拉取最终 run 视图并打印。
//
// 优雅降级：服务端回 501（未启用流式，如进程内 dispatcher 未挂 hub）或流连接失败时，
// 回退到轮询 GET /v1/runs/{id}——保证 --stream 在任何部署形态下都能拿到终态。
func (a *appCtx) streamRun(runID string) error {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sawRunEnd, err := a.consumeSSE(ctx, runID)
	if err != nil {
		// 流不可用：打印一行降级说明，回退轮询。
		a.emitStreamLine(map[string]any{
			"stream": false,
			"note":   "event stream unavailable, falling back to polling",
			"reason": err.Error(),
		})
		final, perr := a.pollRun(runID)
		if perr != nil {
			return perr
		}
		a.emitStreamLine(final)
		return nil
	}

	// 流正常结束：run_end 后再读一次权威终态（含各节点输出汇总）收尾。
	final, perr := a.pollRun(runID)
	if perr != nil {
		if !sawRunEnd {
			return perr
		}
		// 已见 run_end 但终态读取失败：不致命，流本身已完整。
		return nil
	}
	a.emitStreamLine(final)
	return nil
}

// consumeSSE 建立 SSE 连接并逐帧解析，回调式打印事件。
// 返回 (是否见到 run_end, 错误)。501 或连接错误 → 返回错误让调用方回退轮询。
func (a *appCtx) consumeSSE(ctx context.Context, runID string) (bool, error) {
	// 流式请求不能用带 60s 超时的默认 client（会腰斩长连接）；靠 ctx 控制生命周期。
	cli := &http.Client{Timeout: 0}
	url := a.serverURL + "/v1/runs/" + runID + "/events"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return false, newErr("BAD_REQUEST", err.Error(), "")
	}
	req.Header.Set("Accept", "text/event-stream")

	resp, err := cli.Do(req)
	if err != nil {
		return false, newErr("STREAM_UNREACHABLE", err.Error(), "server up? 可去掉 --stream 走轮询")
	}
	defer func() { _ = resp.Body.Close() }()

	// 非 200：多半是 501 STREAM_UNSUPPORTED（或 404 run 不存在）。回退轮询。
	if resp.StatusCode != http.StatusOK {
		return false, newErr("STREAM_UNSUPPORTED",
			fmt.Sprintf("stream endpoint returned HTTP %d", resp.StatusCode), "")
	}

	reader := bufio.NewReader(resp.Body)
	var dataBuf strings.Builder
	sawRunEnd := false
	for {
		line, rerr := reader.ReadString('\n')
		if line != "" {
			trimmed := strings.TrimRight(line, "\r\n")
			switch {
			case trimmed == "":
				// 空行=帧结束：处理累积的 data。
				if dataBuf.Len() > 0 {
					if evt, ok := decodeSSEData(dataBuf.String()); ok {
						a.emitStreamLine(map[string]any{"event": evt})
						if evt.Phase == runevent.PhaseRunEnd {
							sawRunEnd = true
						}
					}
					dataBuf.Reset()
				}
			case strings.HasPrefix(trimmed, ":"):
				// 注释帧（心跳），忽略。
			case strings.HasPrefix(trimmed, "data:"):
				dataBuf.WriteString(strings.TrimSpace(trimmed[len("data:"):]))
			default:
				// id: / event: 行——本地按 data 内的 phase 判定，无需单独解析。
			}
		}
		if rerr != nil {
			// EOF/连接断开：正常收尾（run_end 已发或服务端关流）。
			break
		}
		if sawRunEnd {
			break // 见到 run_end，主动收流（不必等服务端 EOF）
		}
	}
	return sawRunEnd, nil
}

// decodeSSEData 把一条 SSE data 负载解成 RunEvent。
func decodeSSEData(data string) (runevent.RunEvent, bool) {
	var evt runevent.RunEvent
	if err := json.Unmarshal([]byte(data), &evt); err != nil {
		return runevent.RunEvent{}, false
	}
	return evt, true
}

// emitStreamLine 打印一行紧凑的成功 envelope（NDJSON：每行一个 JSON 对象）。
func (a *appCtx) emitStreamLine(data any) {
	b, err := json.Marshal(Envelope{OK: true, Data: data})
	if err != nil {
		return
	}
	fmt.Fprintln(a.out, string(b))
}
