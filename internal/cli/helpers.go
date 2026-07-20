package cli

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"

	"github.com/smart-workflow/smart-workflow/internal/dsl"
)

// mustLoad 加载会话；sid 为空或不存在时返回结构化错误（已写 envelope）。
func mustLoad(a *appCtx, sid string) (*Session, error) {
	if sid == "" {
		return nil, a.emitErr(newErr("BAD_REQUEST", "--sid is required", "先 swf init 拿到 sid"))
	}
	s, err := LoadSession(sid)
	if err != nil {
		return nil, a.emitErr(newErr("SESSION_NOT_FOUND", err.Error(), "检查 sid，或 swf init 新建"))
	}
	return s, nil
}

// genSID 生成短会话 ID。
func genSID() string {
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	return "s_" + hex.EncodeToString(b)
}

// readFile 读文件（薄封装，便于统一错误信息）。
func readFile(path string) ([]byte, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	return b, nil
}

// renderSession 把会话 IR 渲染成 DSL（preview/upload/node-debug 共用）。
func renderSession(s *Session) (*dsl.DSL, error) {
	return dsl.NewRenderer().Render(s.IR)
}
