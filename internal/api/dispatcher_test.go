package api

import (
	"context"
	"testing"
	"time"

	"github.com/smart-workflow/smart-workflow/internal/engine"
)

// Shutdown 后 Submit 必须返回 false（停止接收新 run），避免关停期堆积僵尸 run。
func TestRunDispatcher_SubmitAfterShutdown(t *testing.T) {
	d := NewRunDispatcher(engine.New(nil, ""), nil, 4)

	if err := d.Shutdown(context.Background()); err != nil {
		t.Fatalf("shutdown: %v", err)
	}
	if d.Submit("run_x") {
		t.Fatal("Submit after Shutdown should return false")
	}
}

// Shutdown 在无在途 run 时应立即返回（WaitGroup 空）。
func TestRunDispatcher_ShutdownIdempotentAndFast(t *testing.T) {
	d := NewRunDispatcher(engine.New(nil, ""), nil, 4)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := d.Shutdown(ctx); err != nil {
		t.Fatalf("first shutdown: %v", err)
	}
	// 二次 Shutdown 应幂等、不阻塞。
	if err := d.Shutdown(ctx); err != nil {
		t.Fatalf("second shutdown: %v", err)
	}
}

// 满载时 Submit 背压拒绝：占满 semaphore 坑位后新 Submit 返回 false。
// 用 nil-store engine：engine.Run 会立即返回 error（不 panic），
// 但由于我们先手动占满 sem，此测试聚焦「占坑/拒绝」而非执行结果。
func TestRunDispatcher_BackpressureWhenFull(t *testing.T) {
	d := NewRunDispatcher(engine.New(nil, ""), nil, 1)

	// 手动占满唯一坑位，模拟已有一个 run 在途。
	d.sem <- struct{}{}
	defer func() { <-d.sem }()

	if d.Submit("run_over_capacity") {
		t.Fatal("Submit should be rejected when dispatcher is at capacity")
	}
}
