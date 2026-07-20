package cli

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestCLI_NodeDebug_ForwardsToServer 验证 node-debug：本地渲染 IR→DSL，取出目标
// 节点转发服务端 /v1/node-debug，服务端结果原样进 CLI envelope。
func TestCLI_NodeDebug_ForwardsToServer(t *testing.T) {
	t.Setenv("SWF_SESSIONS_DIR", t.TempDir())

	var gotNodeType string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/node-debug" && r.Method == http.MethodPost {
			var req struct {
				Node struct {
					Data struct {
						NodeMeta struct {
							NodeType string `json:"nodeType"`
						} `json:"nodeMeta"`
					} `json:"data"`
				} `json:"node"`
			}
			_ = json.NewDecoder(r.Body).Decode(&req)
			gotNodeType = req.Node.Data.NodeMeta.NodeType
			_ = json.NewEncoder(w).Encode(map[string]any{
				"ok": true,
				"data": map[string]any{
					"node_id": "code::x", "node_type": "code", "status": "succeeded",
					"output":     map[string]any{"doubled": 42},
					"assertions": []any{map[string]any{"type": "status_success", "pass": true}},
				},
			})
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	env, _ := runCLI(t, "init", "--name", "d")
	sid := dataField(t, env, "sid").(string)
	if _, err := runCLI(t, "add-node", "--sid", sid, "--id", "code_0", "--kind", "code"); err != nil {
		t.Fatalf("add-node: %v", err)
	}

	env, err := runCLI(t, "node-debug", "--server", srv.URL, "--sid", sid, "--node-id", "code_0", "--inputs", `{"n":21}`)
	if err != nil || !env.OK {
		t.Fatalf("node-debug failed: err=%v env=%+v", err, env)
	}
	if gotNodeType != "code" {
		t.Fatalf("server received node type %q, want code", gotNodeType)
	}
	if st, _ := dataField(t, env, "status").(string); st != "succeeded" {
		t.Fatalf("status = %q, want succeeded", st)
	}
}

// TestCLI_Run_PropagatesValidationError 验证服务端 422 VALIDATION_FAILED（TD-10 gate）
// 被 CLI 原样转成结构化错误 envelope（含 details.issues），Agent 可据此定位修复。
func TestCLI_Run_PropagatesValidationError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/runs" && r.Method == http.MethodPost {
			w.WriteHeader(http.StatusUnprocessableEntity)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"ok": false,
				"error": map[string]any{
					"code": "VALIDATION_FAILED", "message": "workflow validation failed with 1 error(s)",
					"details": map[string]any{"issues": []any{
						map[string]any{"code": "start_count", "severity": "error", "message": "no start"},
					}},
				},
			})
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	env, err := runCLI(t, "run", "--server", srv.URL, "--workflow-id", "wf_x", "--input", `{"q":"hi"}`)
	if err == nil {
		t.Fatal("expected non-nil error (non-zero exit)")
	}
	if env.OK || env.Error == nil || env.Error.Code != "VALIDATION_FAILED" {
		t.Fatalf("expected VALIDATION_FAILED envelope, got %+v", env)
	}
	// details.issues 应透传。
	details, ok := env.Error.Details.(map[string]any)
	if !ok || details["issues"] == nil {
		t.Fatalf("expected details.issues passthrough, got %+v", env.Error.Details)
	}
}

// TestCLI_Upload_CreatesNewCopy 验证 upload 默认创建新副本，回填 workflow_id。
func TestCLI_Upload_CreatesNewCopy(t *testing.T) {
	t.Setenv("SWF_SESSIONS_DIR", t.TempDir())

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/workflows" && r.Method == http.MethodPost {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"ok": true, "data": map[string]any{"workflow_id": "wf_new_1"},
			})
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	env, _ := runCLI(t, "init", "--name", "up", "--project-id", "6970")
	sid := dataField(t, env, "sid").(string)
	_, _ = runCLI(t, "add-node", "--sid", sid, "--id", "start_0", "--kind", "start", "--outputs", "q:string")
	_, _ = runCLI(t, "add-node", "--sid", sid, "--id", "end_0", "--kind", "end")
	_, _ = runCLI(t, "add-edge", "--sid", sid, "--source", "start_0", "--target", "end_0")

	env, err := runCLI(t, "upload", "--server", srv.URL, "--sid", sid, "--description", "agent draft")
	if err != nil || !env.OK {
		t.Fatalf("upload failed: err=%v env=%+v", err, env)
	}
	if wid, _ := dataField(t, env, "workflow_id").(string); wid != "wf_new_1" {
		t.Fatalf("workflow_id = %v, want wf_new_1", dataField(t, env, "workflow_id"))
	}
}

// TestCLI_Upload_UpdateNeedsConfirm 验证 --update-id 覆盖更新必须 --confirm。
func TestCLI_Upload_UpdateNeedsConfirm(t *testing.T) {
	t.Setenv("SWF_SESSIONS_DIR", t.TempDir())
	env, _ := runCLI(t, "init", "--name", "up")
	sid := dataField(t, env, "sid").(string)
	_, _ = runCLI(t, "add-node", "--sid", sid, "--id", "start_0", "--kind", "start")

	env, err := runCLI(t, "upload", "--sid", sid, "--update-id", "wf_1")
	if err == nil {
		t.Fatal("update without --confirm should fail")
	}
	if env.OK || env.Error == nil || env.Error.Code != "CONFIRM_REQUIRED" {
		t.Fatalf("expected CONFIRM_REQUIRED, got %+v", env)
	}
}

// TestCLI_Run_ServerUnreachable 验证服务端不可达时返回结构化 SERVER_UNREACHABLE。
func TestCLI_Run_ServerUnreachable(t *testing.T) {
	// 指向一个几乎不可能有服务的地址。
	env, err := runCLI(t, "run", "--server", "http://127.0.0.1:1", "--workflow-id", "wf_x")
	if err == nil {
		t.Fatal("expected error on unreachable server")
	}
	if env.OK || env.Error == nil || env.Error.Code != "SERVER_UNREACHABLE" {
		t.Fatalf("expected SERVER_UNREACHABLE, got %+v", env)
	}
}
