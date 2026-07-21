package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// streamLines 把 NDJSON 输出按行解析成 envelope 列表（跳过空行）。
func streamLines(t *testing.T, out string) []Envelope {
	t.Helper()
	var envs []Envelope
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var env Envelope
		if err := json.Unmarshal([]byte(line), &env); err != nil {
			t.Fatalf("bad NDJSON line %q: %v", line, err)
		}
		envs = append(envs, env)
	}
	return envs
}

// sseFrame 拼一条 SSE 事件帧（event + data + 空行）。
func sseFrame(phase, data string) string {
	return fmt.Sprintf("event: %s\ndata: %s\n\n", phase, data)
}

// TestCLI_StreamRun_ConsumesEventsThenFinal 验证 --stream 正常路径：
// 逐条打印 SSE 事件（node_start/node_end/run_end），末尾再打印一次终态 run 视图。
func TestCLI_StreamRun_ConsumesEventsThenFinal(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/events"):
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)
			flush, _ := w.(http.Flusher)
			frames := []string{
				sseFrame("node_start", `{"run_id":"run1","seq":1,"phase":"node_start","node_id":"start::1","ts":1}`),
				sseFrame("node_end", `{"run_id":"run1","seq":2,"phase":"node_end","node_id":"start::1","status":"succeeded","ts":2}`),
				sseFrame("node_end", `{"run_id":"run1","seq":3,"phase":"node_end","node_id":"end::1","status":"succeeded","output":{"output":"hi"},"ts":3}`),
				sseFrame("run_end", `{"run_id":"run1","seq":4,"phase":"run_end","status":"succeeded","ts":4}`),
			}
			for _, f := range frames {
				_, _ = w.Write([]byte(f))
				if flush != nil {
					flush.Flush()
				}
			}
		default: // GET /v1/runs/run1 终态视图
			_ = json.NewEncoder(w).Encode(map[string]any{
				"ok": true,
				"data": map[string]any{
					"run_id": "run1", "status": "succeeded",
					"output": map[string]any{"output": "hi"},
				},
			})
		}
	}))
	defer srv.Close()

	var buf bytes.Buffer
	app := &appCtx{out: &buf, serverURL: srv.URL}
	if err := app.streamRun("run1"); err != nil {
		t.Fatalf("streamRun: %v", err)
	}

	envs := streamLines(t, buf.String())
	// 4 条事件 + 1 条终态视图。
	if len(envs) != 5 {
		t.Fatalf("got %d NDJSON lines, want 5: %s", len(envs), buf.String())
	}
	// 前 4 条应是事件，含 run_end。
	sawRunEnd := false
	for i := 0; i < 4; i++ {
		m, ok := envs[i].Data.(map[string]any)
		if !ok || m["event"] == nil {
			t.Fatalf("line %d not an event envelope: %+v", i, envs[i])
		}
		evt := m["event"].(map[string]any)
		if evt["phase"] == "run_end" {
			sawRunEnd = true
		}
	}
	if !sawRunEnd {
		t.Fatal("run_end event not printed")
	}
	// 最后一条应是终态 run 视图（有 status=succeeded，无 event 字段）。
	last, ok := envs[4].Data.(map[string]any)
	if !ok || last["status"] != "succeeded" {
		t.Fatalf("final line not terminal run view: %+v", envs[4])
	}
}

// TestCLI_StreamRun_FallsBackToPollingOn501 验证流不可用（501）时自动回退轮询：
// 打印一条降级说明 + 最终终态视图，不报错。
func TestCLI_StreamRun_FallsBackToPollingOn501(t *testing.T) {
	polled := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/events") {
			w.WriteHeader(http.StatusNotImplemented)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"ok": false,
				"error": map[string]any{"code": "STREAM_UNSUPPORTED", "message": "not enabled"},
			})
			return
		}
		// GET /v1/runs/run1 轮询：直接给终态。
		polled = true
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok":   true,
			"data": map[string]any{"run_id": "run1", "status": "succeeded"},
		})
	}))
	defer srv.Close()

	var buf bytes.Buffer
	app := &appCtx{out: &buf, serverURL: srv.URL}
	if err := app.streamRun("run1"); err != nil {
		t.Fatalf("streamRun should not error on fallback: %v", err)
	}
	if !polled {
		t.Fatal("expected polling fallback to hit GET /v1/runs/run1")
	}

	envs := streamLines(t, buf.String())
	if len(envs) < 2 {
		t.Fatalf("want >=2 lines (fallback note + final), got %d: %s", len(envs), buf.String())
	}
	// 第一行是降级说明。
	first, _ := envs[0].Data.(map[string]any)
	if first["stream"] != false {
		t.Fatalf("first line should be fallback note with stream=false: %+v", envs[0])
	}
	// 末行终态。
	last, _ := envs[len(envs)-1].Data.(map[string]any)
	if last["status"] != "succeeded" {
		t.Fatalf("last line should be terminal run view: %+v", envs[len(envs)-1])
	}
}
