package cli

import (
	"bytes"
	"encoding/json"
	"testing"
)

// runCLI 用给定 args 跑一次根命令，返回解析后的 envelope 与原始 error。
// out 注入 buffer 以捕获 envelope；SWF_SESSIONS_DIR 由调用方用 t.Setenv 隔离。
func runCLI(t *testing.T, args ...string) (Envelope, error) {
	t.Helper()
	var buf bytes.Buffer
	root := NewRootCmd(&buf)
	root.SetArgs(args)
	root.SetOut(&buf)
	root.SetErr(&buf)
	err := root.Execute()

	var env Envelope
	if buf.Len() > 0 {
		if jerr := json.Unmarshal(buf.Bytes(), &env); jerr != nil {
			t.Fatalf("unmarshal envelope failed: %v; raw=%s", jerr, buf.String())
		}
	}
	return env, err
}

// dataStr 从 envelope.Data（map）取一个字符串字段。
func dataField(t *testing.T, env Envelope, key string) any {
	t.Helper()
	m, ok := env.Data.(map[string]any)
	if !ok {
		t.Fatalf("data is not a map: %T", env.Data)
	}
	return m[key]
}

// TestCLI_FullOfflineBuildLoop 验证 P0 评测闭环全在进程内跑通：
// init → add-node ×3 → add-edge ×2 → bind ×3 → validate(0 error) → preview。
// 这正是 M7 验收的"建图→bind→validate"最小闭环（不碰服务端）。
func TestCLI_FullOfflineBuildLoop(t *testing.T) {
	t.Setenv("SWF_SESSIONS_DIR", t.TempDir())

	env, err := runCLI(t, "init", "--name", "情感评测", "--project-id", "6970")
	if err != nil || !env.OK {
		t.Fatalf("init failed: err=%v env=%+v", err, env)
	}
	sid, _ := dataField(t, env, "sid").(string)
	if sid == "" {
		t.Fatal("empty sid")
	}

	mustOK := func(args ...string) {
		env, err := runCLI(t, args...)
		if err != nil || !env.OK {
			t.Fatalf("cmd %v failed: err=%v env=%+v", args, err, env)
		}
	}
	mustOK("add-node", "--sid", sid, "--id", "start_0", "--kind", "start", "--outputs", "query:string:required")
	mustOK("add-node", "--sid", sid, "--id", "app_1", "--kind", "application", "--app-id", "12345", "--title", "情感分类器",
		"--inputs", "text:string:required", "--outputs", "label:string,confidence:number")
	mustOK("add-node", "--sid", sid, "--id", "end_0", "--kind", "end", "--inputs", "label:string,confidence:number")
	mustOK("add-edge", "--sid", sid, "--source", "start_0", "--target", "app_1")
	mustOK("add-edge", "--sid", sid, "--source", "app_1", "--target", "end_0")
	mustOK("bind", "--sid", sid, "--node-id", "app_1", "--port", "text", "--mode", "ref", "--source-node", "start_0", "--source-port", "query")
	mustOK("bind", "--sid", sid, "--node-id", "end_0", "--port", "label", "--mode", "ref", "--source-node", "app_1", "--source-port", "label")
	mustOK("bind", "--sid", sid, "--node-id", "end_0", "--port", "confidence", "--mode", "ref", "--source-node", "app_1", "--source-port", "confidence")

	// validate：应 0 error。
	env, err = runCLI(t, "validate", "--sid", sid)
	if err != nil || !env.OK {
		t.Fatalf("validate failed: err=%v env=%+v", err, env)
	}
	if hasErr, _ := dataField(t, env, "has_error").(bool); hasErr {
		t.Fatalf("expected 0 error, got issues: %+v", env.Data)
	}

	// preview：3 节点 2 边。
	env, err = runCLI(t, "preview", "--sid", sid)
	if err != nil || !env.OK {
		t.Fatalf("preview failed: err=%v env=%+v", err, env)
	}
	if n, _ := dataField(t, env, "node_num").(float64); n != 3 {
		t.Fatalf("node_num = %v, want 3", dataField(t, env, "node_num"))
	}
}

// TestCLI_ValidateCatchesBadGraph 验证坏图（缺 start/end + 环）被 validate 拦下，
// 且返回结构化 issues（Agent 据此定位修复）。
func TestCLI_ValidateCatchesBadGraph(t *testing.T) {
	t.Setenv("SWF_SESSIONS_DIR", t.TempDir())
	env, _ := runCLI(t, "init", "--name", "bad")
	sid := dataField(t, env, "sid").(string)

	_, _ = runCLI(t, "add-node", "--sid", sid, "--id", "a", "--kind", "application", "--app-id", "1")
	_, _ = runCLI(t, "add-node", "--sid", sid, "--id", "b", "--kind", "application", "--app-id", "2")
	_, _ = runCLI(t, "add-edge", "--sid", sid, "--source", "a", "--target", "b")
	_, _ = runCLI(t, "add-edge", "--sid", sid, "--source", "b", "--target", "a")

	env, err := runCLI(t, "validate", "--sid", sid)
	if err != nil || !env.OK {
		t.Fatalf("validate should run ok even on bad graph: err=%v env=%+v", err, env)
	}
	if hasErr, _ := dataField(t, env, "has_error").(bool); !hasErr {
		t.Fatal("bad graph should have has_error=true")
	}
	// 至少应含 start_count / cycle。
	m := env.Data.(map[string]any)
	issues, _ := m["issues"].([]any)
	if len(issues) < 2 {
		t.Fatalf("expected multiple issues, got %+v", issues)
	}
}

// TestCLI_StructuredErrors 验证各类错误都走结构化 envelope + 非零退出码。
func TestCLI_StructuredErrors(t *testing.T) {
	t.Setenv("SWF_SESSIONS_DIR", t.TempDir())

	cases := []struct {
		name     string
		args     []string
		wantCode string
	}{
		{"init without name", []string{"init"}, "BAD_REQUEST"},
		{"add-node bad sid", []string{"add-node", "--sid", "ghost", "--id", "x", "--kind", "start"}, "SESSION_NOT_FOUND"},
		{"validate without sid", []string{"validate"}, "BAD_REQUEST"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env, err := runCLI(t, tc.args...)
			if err == nil {
				t.Fatal("expected non-nil error (non-zero exit)")
			}
			if env.OK || env.Error == nil {
				t.Fatalf("expected error envelope, got %+v", env)
			}
			if env.Error.Code != tc.wantCode {
				t.Fatalf("code = %q, want %q", env.Error.Code, tc.wantCode)
			}
		})
	}
}

// TestCLI_AddNodePortParsing 验证 --inputs/--outputs 端口声明解析（风险5 入口）。
func TestCLI_AddNodePortParsing(t *testing.T) {
	t.Setenv("SWF_SESSIONS_DIR", t.TempDir())
	env, _ := runCLI(t, "init", "--name", "p")
	sid := dataField(t, env, "sid").(string)

	// 坏端口格式应 400。
	env, err := runCLI(t, "add-node", "--sid", sid, "--id", "n", "--kind", "start", "--outputs", "badspec")
	if err == nil || env.OK || env.Error.Code != "BAD_REQUEST" {
		t.Fatalf("bad port spec should fail with BAD_REQUEST, got env=%+v err=%v", env, err)
	}

	// 正确格式：加载会话确认端口落地 + required 标志。
	if _, err := runCLI(t, "add-node", "--sid", sid, "--id", "s0", "--kind", "start", "--outputs", "query:string:required,extra:number"); err != nil {
		t.Fatalf("add-node failed: %v", err)
	}
	s, lerr := LoadSession(sid)
	if lerr != nil {
		t.Fatalf("load: %v", lerr)
	}
	node := s.IR.FindNode("s0")
	if node == nil || len(node.Outputs) != 2 {
		t.Fatalf("outputs not parsed: %+v", node)
	}
	if node.Outputs[0].Name != "query" || node.Outputs[0].Type != "string" || !node.Outputs[0].Required {
		t.Fatalf("output[0] = %+v", node.Outputs[0])
	}
	if node.Outputs[1].Required {
		t.Fatalf("output[1] should not be required: %+v", node.Outputs[1])
	}
}
