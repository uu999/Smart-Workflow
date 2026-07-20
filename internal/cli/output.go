package cli

import (
	"encoding/json"
	"fmt"
	"io"
)

// Envelope 是 CLI 统一输出（设计文档 §3）。与服务端 httpx.Envelope 同形，
// 便于 Agent 无论调 CLI 还是直连 API 都用同一套解析。
//
//	成功: { "ok": true,  "data": {...} }
//	失败: { "ok": false, "error": { "code": "...", "message": "...", "hint": "...", "details": {...} } }
type Envelope struct {
	OK    bool     `json:"ok"`
	Data  any      `json:"data,omitempty"`
	Error *CLIErr  `json:"error,omitempty"`
}

// CLIErr 是结构化错误。hint 是面向 Agent 的下一步建议（如"先 swf add-node"）。
type CLIErr struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Hint    string `json:"hint,omitempty"`
	Details any    `json:"details,omitempty"`
}

// Error 实现 error 接口，便于在命令间以 error 形式传播结构化错误。
func (e *CLIErr) Error() string { return fmt.Sprintf("%s: %s", e.Code, e.Message) }

// newErr 构造一个带 code/message/hint 的结构化错误。
func newErr(code, message, hint string) *CLIErr {
	return &CLIErr{Code: code, Message: message, Hint: hint}
}

// writeOK 把成功结果以 envelope 打到 w。
func writeOK(w io.Writer, data any) error {
	return writeEnvelope(w, Envelope{OK: true, Data: data})
}

// writeErr 把结构化错误以 envelope 打到 w。非 *CLIErr 归一为 INTERNAL。
func writeErr(w io.Writer, err error) error {
	ce, ok := err.(*CLIErr)
	if !ok {
		ce = &CLIErr{Code: "INTERNAL", Message: err.Error()}
	}
	return writeEnvelope(w, Envelope{OK: false, Error: ce})
}

// writeEnvelope 以缩进 JSON 输出 envelope（Agent 友好、人也可读）。
func writeEnvelope(w io.Writer, env Envelope) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(env)
}
