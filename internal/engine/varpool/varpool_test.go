package varpool

import (
	"testing"

	"github.com/smart-workflow/smart-workflow/internal/dsl"
)

// 对标 PaiFlow VariablePoolTest.basicTest：基本数据类型。
func TestPool_BasicTypes(t *testing.T) {
	p := New()
	p.Set("node-start::001", "user_input", "请介绍一下Java")
	p.Set("node-start::001", "request_id", 12345)
	p.Set("node-start::001", "is_urgent", true)

	if v, _ := p.Get("node-start::001", "user_input"); v != "请介绍一下Java" {
		t.Errorf("user_input = %v", v)
	}
	if v, _ := p.Get("node-start::001", "request_id"); v != 12345 {
		t.Errorf("request_id = %v", v)
	}
	if v, _ := p.Get("node-start::001", "is_urgent"); v != true {
		t.Errorf("is_urgent = %v", v)
	}
}

// 对标 PaiFlow objTest：对象成员访问 response.metadata.model。
func TestPool_ObjectPath(t *testing.T) {
	p := New()
	p.Set("node-llm::002", "response", map[string]any{
		"content":   "Java是一种面向对象的编程语言...",
		"wordCount": 150,
		"metadata": map[string]any{
			"model":       "deepseek-chat",
			"temperature": 0.7,
		},
	})

	if v, err := p.Get("node-llm::002", "response.content"); err != nil || v != "Java是一种面向对象的编程语言..." {
		t.Errorf("content = %v, err=%v", v, err)
	}
	if v, err := p.Get("node-llm::002", "response.wordCount"); err != nil || v != 150 {
		t.Errorf("wordCount = %v, err=%v", v, err)
	}
	if v, err := p.Get("node-llm::002", "response.metadata.model"); err != nil || v != "deepseek-chat" {
		t.Errorf("model = %v, err=%v", v, err)
	}
	if v, err := p.Get("node-llm::002", "response.metadata.temperature"); err != nil || v != 0.7 {
		t.Errorf("temperature = %v, err=%v", v, err)
	}
}

// 对标 PaiFlow listTest：数组索引 result.segments[0].text。
func TestPool_ArrayIndexPath(t *testing.T) {
	p := New()
	p.Set("node-tts::003", "result", map[string]any{
		"segments": []any{
			map[string]any{"text": "欢迎收听技术播客", "speaker": "xiaoyan", "duration": 3000},
			map[string]any{"text": "今天我们要讨论Java", "speaker": "yihui", "duration": 2500},
		},
		"total_duration": 5500,
	})

	if v, err := p.Get("node-tts::003", "result.segments[0].text"); err != nil || v != "欢迎收听技术播客" {
		t.Errorf("seg0.text = %v, err=%v", v, err)
	}
	if v, err := p.Get("node-tts::003", "result.segments[1].speaker"); err != nil || v != "yihui" {
		t.Errorf("seg1.speaker = %v, err=%v", v, err)
	}
	if v, err := p.Get("node-tts::003", "result.segments[0].duration"); err != nil || v != 3000 {
		t.Errorf("seg0.duration = %v, err=%v", v, err)
	}
}

func TestPool_Errors(t *testing.T) {
	p := New()
	p.Set("n", "arr", []any{1, 2})
	p.Set("n", "obj", map[string]any{"a": 1})

	cases := []struct{ node, path string }{
		{"missing", "x"},               // 节点不存在
		{"n", "nope"},                  // 端口不存在
		{"n", "arr[5]"},                // 越界
		{"n", "obj.b"},                 // key 不存在
		{"n", "arr.x"},                 // 对数组用字段
		{"n", "obj[0]"},                // 对对象用索引
	}
	for _, c := range cases {
		if _, err := p.Get(c.node, c.path); err == nil {
			t.Errorf("expected error for %s.%s", c.node, c.path)
		}
	}
}

func TestPool_SystemErrorVars(t *testing.T) {
	p := New()
	p.SetError("app::1", 500, "boom")
	if v, _ := p.Get("app::1", SysErrorCode); v != 500 {
		t.Errorf("errorCode = %v", v)
	}
	if v, _ := p.Get("app::1", SysErrorMessage); v != "boom" {
		t.Errorf("errorMessage = %v", v)
	}
}

func TestPool_Snapshot(t *testing.T) {
	p := New()
	p.Set("n", "k", "v")
	snap := p.Snapshot()
	snap["n"]["k"] = "mutated" // 改快照不应影响池
	if v, _ := p.Get("n", "k"); v != "v" {
		t.Errorf("snapshot not isolated: %v", v)
	}
}

func TestValidateOutputs(t *testing.T) {
	decls := []dsl.Port{
		{Name: "label", Type: dsl.ValueTypeString, Required: true},
		{Name: "score", Type: dsl.ValueTypeNumber},
		{Name: "count", Type: dsl.ValueTypeInteger},
	}

	// 全部正确。
	if p := ValidateOutputs(map[string]any{"label": "ok", "score": 0.9, "count": float64(3)}, decls); len(p) != 0 {
		t.Errorf("expected pass, got %v", p)
	}
	// 缺必填。
	if p := ValidateOutputs(map[string]any{"score": 0.9}, decls); len(p) == 0 {
		t.Error("expected missing required label")
	}
	// 类型不符。
	if p := ValidateOutputs(map[string]any{"label": 123}, decls); len(p) == 0 {
		t.Error("expected type mismatch for label")
	}
	// 非整数。
	if p := ValidateOutputs(map[string]any{"label": "x", "count": 3.5}, decls); len(p) == 0 {
		t.Error("expected integer mismatch for count")
	}
}
