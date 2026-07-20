package nodes

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// TestContract_CodeRunResponse 是 Go↔Python 契约的最小守卫。
//
// 背景（M5 反思③）：Go 侧 codeRunResponse 与 Python sidecar 的 envelope
// 是两边各写一份、靠肉眼对齐的。任一边改字段名，另一边会静默解析出零值而不报错。
// 这里用一份共享 golden fixture（sidecar/contract/code_run.golden.json，
// Python 侧同样以它为契约基准）双向锁定字段结构：
//
//	成功: {ok, data:{outputs, logs}}
//	失败: {ok, error:{code, message}}
//
// 若字段被改名，本测试会失败，把静默 bug 变成显式的 CI 失败。
func TestContract_CodeRunResponse(t *testing.T) {
	path := filepath.Join("..", "..", "..", "sidecar", "contract", "code_run.golden.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden fixture: %v", err)
	}

	var golden struct {
		Success json.RawMessage `json:"success_response"`
		Error   json.RawMessage `json:"error_response"`
	}
	if err := json.Unmarshal(raw, &golden); err != nil {
		t.Fatalf("unmarshal golden: %v", err)
	}

	// 成功响应：字段必须逐一映射到 codeRunResponse。
	var ok codeRunResponse
	if err := strictUnmarshal(golden.Success, &ok); err != nil {
		t.Fatalf("success response does not match Go contract: %v", err)
	}
	if !ok.OK {
		t.Fatal("success: ok should be true")
	}
	if got := ok.Data.Outputs["answer"]; got != float64(42) {
		t.Fatalf("success: data.outputs.answer = %v, want 42", got)
	}
	if ok.Data.Logs != "debug line\n" {
		t.Fatalf("success: data.logs = %q, want the fixture log line", ok.Data.Logs)
	}

	// 失败响应：error.code / error.message 必须映射。
	var bad codeRunResponse
	if err := strictUnmarshal(golden.Error, &bad); err != nil {
		t.Fatalf("error response does not match Go contract: %v", err)
	}
	if bad.OK {
		t.Fatal("error: ok should be false")
	}
	if bad.Error == nil {
		t.Fatal("error: error object should be present")
	}
	if bad.Error.Code != "RUNTIME_ERROR" || bad.Error.Message == "" {
		t.Fatalf("error: got code=%q message=%q", bad.Error.Code, bad.Error.Message)
	}
}

// strictUnmarshal 用 DisallowUnknownFields 保证 golden 里的字段都能被 Go 结构接住；
// 反过来若 golden 出现 Go 未声明的字段（如 Python 新增字段未同步），会报错，
// 提示 Go 侧需要跟进契约变更。
func strictUnmarshal(data []byte, v any) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	return dec.Decode(v)
}
