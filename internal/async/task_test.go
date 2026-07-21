package async

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/hibiken/asynq"
)

func TestNewRunTask_Options(t *testing.T) {
	task, err := NewRunTask("run_1", QueueBatch, 5)
	if err != nil {
		t.Fatalf("NewRunTask: %v", err)
	}
	if task.Type() != TypeRunWorkflow {
		t.Errorf("type = %q, want %q", task.Type(), TypeRunWorkflow)
	}
	// payload 应可解回 runID。
	var p RunPayload
	if err := json.Unmarshal(task.Payload(), &p); err != nil {
		t.Fatalf("payload unmarshal: %v", err)
	}
	if p.RunID != "run_1" {
		t.Errorf("run_id = %q, want run_1", p.RunID)
	}
}

func TestNewRunTask_EmptyRunID(t *testing.T) {
	if _, err := NewRunTask("", QueueDefault, 3); err == nil {
		t.Fatal("empty runID should error")
	}
}

func TestNewRunTask_DefaultQueue(t *testing.T) {
	// 空队列名应回落 default（构造不报错即可，队列名内嵌 opts 无法直接读，靠不 panic 验证）。
	if _, err := NewRunTask("run_1", "", 3); err != nil {
		t.Fatalf("empty queue should default, got err: %v", err)
	}
}

// fakeExec 记录被调用的 runID 并可注入错误。
type fakeExec struct {
	gotRunID string
	err      error
}

func (f *fakeExec) Run(_ context.Context, runID string) error {
	f.gotRunID = runID
	return f.err
}

func TestRunProcessor_Success(t *testing.T) {
	fe := &fakeExec{}
	p := NewRunProcessor(fe)
	task, _ := NewRunTask("run_ok", QueueDefault, 3)

	if err := p.ProcessTask(context.Background(), task); err != nil {
		t.Fatalf("ProcessTask: %v", err)
	}
	if fe.gotRunID != "run_ok" {
		t.Errorf("exec called with %q, want run_ok", fe.gotRunID)
	}
}

func TestRunProcessor_ExecErrorRetries(t *testing.T) {
	fe := &fakeExec{err: errors.New("boom")}
	p := NewRunProcessor(fe)
	task, _ := NewRunTask("run_err", QueueDefault, 3)

	err := p.ProcessTask(context.Background(), task)
	if err == nil {
		t.Fatal("exec error should propagate (triggers asynq retry)")
	}
	// 不应被标记 SkipRetry（业务失败要重试）。
	if errors.Is(err, asynq.SkipRetry) {
		t.Error("exec error should NOT be SkipRetry")
	}
}

func TestRunProcessor_BadPayloadSkipsRetry(t *testing.T) {
	fe := &fakeExec{}
	p := NewRunProcessor(fe)
	// 构造一个 payload 坏掉的任务（绕过 NewRunTask）。
	bad := asynq.NewTask(TypeRunWorkflow, []byte("{not json"))

	err := p.ProcessTask(context.Background(), bad)
	if err == nil {
		t.Fatal("bad payload should error")
	}
	if !errors.Is(err, asynq.SkipRetry) {
		t.Errorf("bad payload should be SkipRetry, got %v", err)
	}
	if fe.gotRunID != "" {
		t.Error("exec should not be called on bad payload")
	}
}

func TestQueueWeights(t *testing.T) {
	w := QueueWeights()
	if !(w[QueueDebug] > w[QueueDefault] && w[QueueDefault] > w[QueueBatch]) {
		t.Fatalf("priority order broken: %+v", w)
	}
}

// 编译期确认 RunProcessor 满足 asynq.Handler。
var _ asynq.Handler = (*RunProcessor)(nil)
