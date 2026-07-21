package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/smart-workflow/smart-workflow/internal/httpx"
	"github.com/smart-workflow/smart-workflow/internal/runevent"
	"github.com/smart-workflow/smart-workflow/internal/service"
)

// TestStreamRun_NoSourceReturns501 验证未装配事件源时回 501（CLI 据此回退轮询）。
// 该路径在触库前短路，故无需 store。
func TestStreamRun_NoSourceReturns501(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := &handlers{} // events 为 nil

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodGet, "/v1/runs/run1/events", nil)
	c.Params = gin.Params{{Key: "id", Value: "run1"}}

	h.streamRun(c)

	if rec.Code != http.StatusNotImplemented {
		t.Fatalf("status = %d, want 501; body=%s", rec.Code, rec.Body.String())
	}
	var env httpx.Envelope
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if env.OK || env.Error == nil || env.Error.Code != "STREAM_UNSUPPORTED" {
		t.Fatalf("unexpected envelope: %+v", env)
	}
}

// TestWriteSSEEvent_FrameFormat 锁定 SSE 帧格式：id/event/data 三行 + 空行分隔。
func TestWriteSSEEvent_FrameFormat(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)

	writeSSEEvent(c, runevent.RunEvent{
		RunID: "run1", Seq: 7, Phase: runevent.PhaseNodeStart, NodeID: "n1", NodeType: "http",
	})

	out := rec.Body.String()
	if !strings.HasPrefix(out, "id: 7\n") {
		t.Fatalf("missing id line: %q", out)
	}
	if !strings.Contains(out, "event: node_start\n") {
		t.Fatalf("missing event line: %q", out)
	}
	if !strings.Contains(out, "data: ") || !strings.HasSuffix(out, "\n\n") {
		t.Fatalf("missing data line or terminator: %q", out)
	}
	// data 行须是合法 JSON 且带上事件字段。
	line := extractDataJSON(t, out)
	var evt runevent.RunEvent
	if err := json.Unmarshal([]byte(line), &evt); err != nil {
		t.Fatalf("data not valid JSON: %v; line=%q", err, line)
	}
	if evt.NodeID != "n1" || evt.Phase != runevent.PhaseNodeStart {
		t.Fatalf("roundtrip mismatch: %+v", evt)
	}
}

// TestReplayTerminal 验证「订阅时 run 已终态」的回放：每个节点一条 node_end，
// 末尾补一条 run_end，seq 单调递增，run_end 带 run 终态。
func TestReplayTerminal(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)

	view := &service.RunView{
		RunID:  "run1",
		Status: "succeeded",
		Nodes: []service.NodeRunView{
			{NodeID: "start::1", NodeType: "start", Status: "succeeded", Output: json.RawMessage(`{"q":"hi"}`), CostMs: 1},
			{NodeID: "end::1", NodeType: "end", Status: "succeeded", Output: json.RawMessage(`{"output":"hi"}`), CostMs: 2},
		},
	}
	replayTerminal(c, view)

	events := parseSSEEvents(t, rec.Body.String())
	if len(events) != 3 {
		t.Fatalf("got %d events, want 3 (2 node_end + run_end): %+v", len(events), events)
	}
	if events[0].Phase != runevent.PhaseNodeEnd || events[1].Phase != runevent.PhaseNodeEnd {
		t.Fatalf("first two should be node_end: %+v", events)
	}
	last := events[2]
	if last.Phase != runevent.PhaseRunEnd || last.Status != "succeeded" {
		t.Fatalf("last should be run_end succeeded: %+v", last)
	}
	// seq 单调。
	for i := 1; i < len(events); i++ {
		if events[i].Seq <= events[i-1].Seq {
			t.Fatalf("seq not monotonic: %+v", events)
		}
	}
	// node_end 应携带落库输出（rawToMap 解出）。
	if events[0].Output["q"] != "hi" {
		t.Fatalf("node_end output not replayed: %+v", events[0])
	}
}

func TestIsTerminalStatus(t *testing.T) {
	cases := map[string]bool{
		"succeeded": true, "failed": true, "canceled": true,
		"pending": false, "running": false, "": false,
	}
	for status, want := range cases {
		if got := isTerminalStatus(status); got != want {
			t.Errorf("isTerminalStatus(%q) = %v, want %v", status, got, want)
		}
	}
}

// --- 测试辅助：解析 SSE 帧 ---

// extractDataJSON 从单帧输出里取 data: 行的 JSON 负载。
func extractDataJSON(t *testing.T, frame string) string {
	t.Helper()
	for _, line := range strings.Split(frame, "\n") {
		if strings.HasPrefix(line, "data: ") {
			return strings.TrimPrefix(line, "data: ")
		}
	}
	t.Fatalf("no data line in frame: %q", frame)
	return ""
}

// parseSSEEvents 把多帧 SSE 文本解析成 RunEvent 列表（按 data: 行）。
func parseSSEEvents(t *testing.T, body string) []runevent.RunEvent {
	t.Helper()
	var out []runevent.RunEvent
	for _, line := range strings.Split(body, "\n") {
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		var evt runevent.RunEvent
		if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &evt); err != nil {
			t.Fatalf("bad data line %q: %v", line, err)
		}
		out = append(out, evt)
	}
	return out
}
