package eventbus

import (
	"context"
	"testing"
	"time"

	"github.com/smart-workflow/smart-workflow/internal/runevent"
)

// drainWithin 在超时内收集一个 channel 的全部事件，直到 channel 关闭。
// 返回收到的事件与 channel 是否已关闭（true=正常收敛）。
func drainWithin(t *testing.T, ch <-chan runevent.RunEvent, d time.Duration) ([]runevent.RunEvent, bool) {
	t.Helper()
	var got []runevent.RunEvent
	deadline := time.After(d)
	for {
		select {
		case evt, ok := <-ch:
			if !ok {
				return got, true
			}
			got = append(got, evt)
		case <-deadline:
			return got, false
		}
	}
}

// TestMemHub_PublishAndRunEndCloses 验证：订阅者能收到发布的事件，且 run_end
// 之后 channel 被关闭（SSE 客户端据此收敛断开）。
func TestMemHub_PublishAndRunEndCloses(t *testing.T) {
	h := NewMemHub()
	ch, err := h.Subscribe(context.Background(), "run1")
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}

	h.Emit(runevent.RunEvent{RunID: "run1", Seq: 1, Phase: runevent.PhaseNodeStart, NodeID: "n1"})
	h.Emit(runevent.RunEvent{RunID: "run1", Seq: 2, Phase: runevent.PhaseNodeEnd, NodeID: "n1", Status: "succeeded"})
	h.Emit(runevent.RunEvent{RunID: "run1", Seq: 3, Phase: runevent.PhaseRunEnd, Status: "succeeded"})

	got, closed := drainWithin(t, ch, time.Second)
	if !closed {
		t.Fatal("channel not closed after run_end")
	}
	if len(got) != 3 {
		t.Fatalf("got %d events, want 3: %+v", len(got), got)
	}
	if got[0].Phase != runevent.PhaseNodeStart || got[2].Phase != runevent.PhaseRunEnd {
		t.Fatalf("unexpected event order: %+v", got)
	}
}

// TestMemHub_MultiSubscriber 验证同一 run 的多个订阅者都能独立收到全部事件。
func TestMemHub_MultiSubscriber(t *testing.T) {
	h := NewMemHub()
	ch1, _ := h.Subscribe(context.Background(), "run1")
	ch2, _ := h.Subscribe(context.Background(), "run1")

	h.Emit(runevent.RunEvent{RunID: "run1", Seq: 1, Phase: runevent.PhaseNodeStart, NodeID: "n1"})
	h.Emit(runevent.RunEvent{RunID: "run1", Seq: 2, Phase: runevent.PhaseRunEnd, Status: "succeeded"})

	got1, closed1 := drainWithin(t, ch1, time.Second)
	got2, closed2 := drainWithin(t, ch2, time.Second)
	if !closed1 || !closed2 {
		t.Fatalf("channels not both closed: c1=%v c2=%v", closed1, closed2)
	}
	if len(got1) != 2 || len(got2) != 2 {
		t.Fatalf("each subscriber should get 2 events, got %d/%d", len(got1), len(got2))
	}
}

// TestMemHub_IsolatesRuns 验证事件只发给对应 runID 的订阅者，不串流。
func TestMemHub_IsolatesRuns(t *testing.T) {
	h := NewMemHub()
	chA, _ := h.Subscribe(context.Background(), "runA")
	chB, _ := h.Subscribe(context.Background(), "runB")

	h.Emit(runevent.RunEvent{RunID: "runA", Seq: 1, Phase: runevent.PhaseNodeStart, NodeID: "a"})
	h.Emit(runevent.RunEvent{RunID: "runA", Seq: 2, Phase: runevent.PhaseRunEnd})
	h.Emit(runevent.RunEvent{RunID: "runB", Seq: 1, Phase: runevent.PhaseRunEnd})

	gotA, _ := drainWithin(t, chA, time.Second)
	gotB, _ := drainWithin(t, chB, time.Second)
	if len(gotA) != 2 {
		t.Fatalf("runA subscriber got %d events, want 2: %+v", len(gotA), gotA)
	}
	for _, e := range gotA {
		if e.RunID != "runA" {
			t.Fatalf("runA subscriber leaked event from %s", e.RunID)
		}
	}
	if len(gotB) != 1 {
		t.Fatalf("runB subscriber got %d events, want 1", len(gotB))
	}
}

// TestMemHub_UnsubscribeOnCtxCancel 验证 ctx 取消后订阅者被退订并关闭 channel，
// 且后续 Emit 不会 panic（send on closed channel）。
func TestMemHub_UnsubscribeOnCtxCancel(t *testing.T) {
	h := NewMemHub()
	ctx, cancel := context.WithCancel(context.Background())
	ch, _ := h.Subscribe(ctx, "run1")

	cancel()
	// 等退订 goroutine 关闭 channel。
	if _, closed := drainWithin(t, ch, time.Second); !closed {
		t.Fatal("channel not closed after ctx cancel")
	}

	// 退订后 Emit 不应 panic（该 run 已无订阅者）。
	h.Emit(runevent.RunEvent{RunID: "run1", Seq: 1, Phase: runevent.PhaseNodeStart})
	h.Emit(runevent.RunEvent{RunID: "run1", Seq: 2, Phase: runevent.PhaseRunEnd})
}

// TestMemHub_SlowConsumerDropsButDelivilersRunEnd 验证慢消费者不阻塞发布端：
// 缓冲满时丢中间事件，但 run_end 通过关闭 channel 送达（客户端仍能收敛）。
func TestMemHub_SlowConsumerDoesNotBlockPublisher(t *testing.T) {
	h := NewMemHub()
	ch, _ := h.Subscribe(context.Background(), "run1")

	// 灌超过 subBuffer 的事件；发布端不应阻塞（若阻塞则本测试超时失败）。
	done := make(chan struct{})
	go func() {
		for i := 0; i < subBuffer*3; i++ {
			h.Emit(runevent.RunEvent{RunID: "run1", Seq: int64(i + 1), Phase: runevent.PhaseNodeEnd})
		}
		h.Emit(runevent.RunEvent{RunID: "run1", Seq: int64(subBuffer*3 + 1), Phase: runevent.PhaseRunEnd})
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("publisher blocked on slow consumer")
	}

	// channel 最终应关闭（run_end 送达）。
	_, closed := drainWithin(t, ch, time.Second)
	if !closed {
		t.Fatal("channel not closed; run_end lost")
	}
}

// TestStreamKey 锁定事件流键格式：swf:run:{id}:events（与运行态镜像键同源）。
func TestStreamKey(t *testing.T) {
	if got := StreamKey("abc"); got != "swf:run:abc:events" {
		t.Fatalf("StreamKey = %q, want swf:run:abc:events", got)
	}
}
