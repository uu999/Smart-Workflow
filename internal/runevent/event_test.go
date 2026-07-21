package runevent

import (
	"encoding/json"
	"testing"
)

// TestRunEvent_JSONStable 守卫对外事件契约的字段名稳定（--stream / 前端依赖）。
func TestRunEvent_JSONStable(t *testing.T) {
	evt := RunEvent{
		RunID:    "run_1",
		Seq:      3,
		Phase:    PhaseNodeEnd,
		NodeID:   "app::1",
		NodeType: "application",
		Status:   "succeeded",
		Output:   map[string]any{"label": "pos"},
		CostMs:   1234,
		TS:       1700000000000,
	}
	b, err := json.Marshal(evt)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	for _, k := range []string{"run_id", "seq", "phase", "node_id", "node_type", "status", "output", "cost_ms", "ts"} {
		if _, ok := m[k]; !ok {
			t.Errorf("missing field %q in %s", k, b)
		}
	}
}

// TestRunEvent_OmitEmpty 验证 run_end（无 node 字段）不带空 node_id/output。
func TestRunEvent_OmitEmpty(t *testing.T) {
	b, _ := json.Marshal(RunEvent{RunID: "r", Phase: PhaseRunEnd, Status: "succeeded", TS: 1})
	var m map[string]any
	_ = json.Unmarshal(b, &m)
	if _, ok := m["node_id"]; ok {
		t.Error("run_end should omit node_id")
	}
	if _, ok := m["output"]; ok {
		t.Error("run_end should omit output")
	}
}

func TestNopEmitter(t *testing.T) {
	// 只需不 panic。
	var e Emitter = NopEmitter{}
	e.Emit(RunEvent{RunID: "x", Phase: PhaseRunEnd})
}
