package cli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/smart-workflow/smart-workflow/internal/dsl"
)

// TestSession_SaveLoadRoundTrip 验证会话 IR/meta 落盘再读回无损。
func TestSession_SaveLoadRoundTrip(t *testing.T) {
	t.Setenv("SWF_SESSIONS_DIR", t.TempDir())

	s, err := NewSession("s_test", Meta{Name: "n", ProjectID: "6970"})
	if err != nil {
		t.Fatalf("new session: %v", err)
	}
	s.IR.Nodes = append(s.IR.Nodes, dsl.Node{
		ID: "start_0", Kind: dsl.KindStart,
		Outputs: []dsl.Port{{Name: "query", Type: dsl.ValueTypeString, Required: true}},
	})
	if err := s.Save(); err != nil {
		t.Fatalf("save: %v", err)
	}

	loaded, err := LoadSession("s_test")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if loaded.Meta.Name != "n" || loaded.Meta.ProjectID != "6970" {
		t.Fatalf("meta round-trip lost: %+v", loaded.Meta)
	}
	if len(loaded.IR.Nodes) != 1 || loaded.IR.Nodes[0].ID != "start_0" {
		t.Fatalf("IR round-trip lost: %+v", loaded.IR.Nodes)
	}
	// IR.Meta 应与会话 Meta 同步。
	if loaded.IR.Meta.Name != "n" {
		t.Fatalf("IR.Meta.Name = %q, want n", loaded.IR.Meta.Name)
	}
}

// TestSession_DuplicateFails 验证重复创建同名会话报错（防误覆盖）。
func TestSession_DuplicateFails(t *testing.T) {
	t.Setenv("SWF_SESSIONS_DIR", t.TempDir())
	if _, err := NewSession("dup", Meta{Name: "x"}); err != nil {
		t.Fatalf("first create: %v", err)
	}
	if _, err := NewSession("dup", Meta{Name: "x"}); err == nil {
		t.Fatal("second create should fail")
	}
}

// TestSession_LoadMissing 验证读不存在会话报错。
func TestSession_LoadMissing(t *testing.T) {
	t.Setenv("SWF_SESSIONS_DIR", t.TempDir())
	if _, err := LoadSession("nope"); err == nil {
		t.Fatal("load missing session should fail")
	}
}

// TestSessionsRoot_EnvOverride 验证 SWF_SESSIONS_DIR 覆盖默认根目录。
func TestSessionsRoot_EnvOverride(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("SWF_SESSIONS_DIR", dir)
	if got := SessionsRoot(); got != dir {
		t.Fatalf("SessionsRoot = %q, want %q", got, dir)
	}
	// 会话目录应落在覆盖根下。
	s, err := NewSession("x", Meta{Name: "n"})
	if err != nil {
		t.Fatalf("new session: %v", err)
	}
	if filepath.Dir(s.Dir()) != dir {
		t.Fatalf("session dir %q not under %q", s.Dir(), dir)
	}
	if _, err := os.Stat(filepath.Join(s.Dir(), dirApp)); err != nil {
		t.Fatalf("app_cache dir missing: %v", err)
	}
}

// TestParsePorts 覆盖端口声明解析的各形态。
func TestParsePorts(t *testing.T) {
	cases := []struct {
		in      string
		wantLen int
		wantErr bool
	}{
		{"", 0, false},
		{"a:string", 1, false},
		{"a:string:required,b:number", 2, false},
		{"a:string:true", 1, false},
		{"bad", 0, true},
		{"a:", 0, true},
		{":string", 0, true},
	}
	for _, tc := range cases {
		ports, err := parsePorts(tc.in)
		if tc.wantErr {
			if err == nil {
				t.Errorf("parsePorts(%q) expected error", tc.in)
			}
			continue
		}
		if err != nil {
			t.Errorf("parsePorts(%q) unexpected error: %v", tc.in, err)
			continue
		}
		if len(ports) != tc.wantLen {
			t.Errorf("parsePorts(%q) len = %d, want %d", tc.in, len(ports), tc.wantLen)
		}
	}
	// required 语义。
	ports, _ := parsePorts("a:string:required")
	if !ports[0].Required {
		t.Error("':required' should set Required=true")
	}
}
