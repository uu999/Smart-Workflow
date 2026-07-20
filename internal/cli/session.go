// Package cli 实现面向 Agent 的 swf 命令行（设计文档 §3）。
//
// 分层：
//   - session：IR/DSL 两层的会话目录读写（ir.json/meta.json/dsl.json/app_cache）
//   - output：统一 JSON envelope（{ok,data} / {ok,error{code,message,hint}}）
//   - client：调服务端 HTTP（node-debug / run / upload）
//   - 各命令：cobra 子命令，本地操作 IR（init/add-*/bind/validate/preview），
//     真跑类命令（node-debug/run/upload）转发服务端
//
// 设计闭环（风险1 决策）：validate/preview 在 CLI 进程内跑（零成本离线），
// node-debug/run 才碰服务端；"upload 前先修到 0 error" 得以成立。
package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/smart-workflow/smart-workflow/internal/dsl"
)

// Session 是一次工作流构建会话，持有编辑态 IR 与元信息。
type Session struct {
	ID   string   `json:"-"`
	Meta Meta     `json:"-"` // 落 meta.json
	IR   *dsl.IR  `json:"-"` // 落 ir.json
	dir  string   // 会话目录绝对路径
}

// Meta 是会话元信息（落 meta.json），记录名称/项目/来源，供 upload 用。
type Meta struct {
	Name      string `json:"name"`
	ProjectID string `json:"project_id,omitempty"`
	Source    string `json:"source,omitempty"`     // clone-ref 来源工作流 ID
	CreatedAt string `json:"created_at,omitempty"`
	UpdatedAt string `json:"updated_at,omitempty"`
}

const (
	fileIR   = "ir.json"
	fileMeta = "meta.json"
	fileDSL  = "dsl.json"
	dirApp   = "app_cache"
)

// SessionsRoot 返回会话根目录：$SWF_SESSIONS_DIR，缺省 $HOME/tmp/swf/sessions。
func SessionsRoot() string {
	if v := os.Getenv("SWF_SESSIONS_DIR"); v != "" {
		return v
	}
	home, err := os.UserHomeDir()
	if err != nil {
		home = os.TempDir()
	}
	return filepath.Join(home, "tmp", "swf", "sessions")
}

// sessionDir 拼一个会话的目录路径。
func sessionDir(sid string) string {
	return filepath.Join(SessionsRoot(), sid)
}

// NewSession 在磁盘上创建一个新会话（含空 IR 骨架）并落盘。
func NewSession(sid string, meta Meta) (*Session, error) {
	dir := sessionDir(sid)
	if _, err := os.Stat(dir); err == nil {
		return nil, fmt.Errorf("session %q already exists", sid)
	}
	if err := os.MkdirAll(filepath.Join(dir, dirApp), 0o755); err != nil {
		return nil, fmt.Errorf("create session dir: %w", err)
	}
	now := time.Now().UTC().Format(time.RFC3339)
	meta.CreatedAt = now
	meta.UpdatedAt = now
	s := &Session{
		ID:   sid,
		Meta: meta,
		IR:   &dsl.IR{Meta: dsl.Meta{Name: meta.Name, ProjectID: meta.ProjectID, Source: meta.Source}},
		dir:  dir,
	}
	if err := s.Save(); err != nil {
		return nil, err
	}
	return s, nil
}

// LoadSession 从磁盘读取一个已存在的会话。
func LoadSession(sid string) (*Session, error) {
	dir := sessionDir(sid)
	if _, err := os.Stat(dir); err != nil {
		return nil, fmt.Errorf("session %q not found (looked in %s)", sid, dir)
	}
	s := &Session{ID: sid, dir: dir}

	metaRaw, err := os.ReadFile(filepath.Join(dir, fileMeta))
	if err != nil {
		return nil, fmt.Errorf("read meta.json: %w", err)
	}
	if err := json.Unmarshal(metaRaw, &s.Meta); err != nil {
		return nil, fmt.Errorf("parse meta.json: %w", err)
	}

	irRaw, err := os.ReadFile(filepath.Join(dir, fileIR))
	if err != nil {
		return nil, fmt.Errorf("read ir.json: %w", err)
	}
	var ir dsl.IR
	if err := json.Unmarshal(irRaw, &ir); err != nil {
		return nil, fmt.Errorf("parse ir.json: %w", err)
	}
	s.IR = &ir
	return s, nil
}

// Save 把 IR 与 meta 落盘（原子写：先写临时文件再 rename）。
func (s *Session) Save() error {
	s.Meta.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	// IR.Meta 与会话 Meta 保持同步（name/project/source）。
	s.IR.Meta.Name = s.Meta.Name
	s.IR.Meta.ProjectID = s.Meta.ProjectID
	s.IR.Meta.Source = s.Meta.Source

	if err := writeJSONAtomic(filepath.Join(s.dir, fileMeta), s.Meta); err != nil {
		return err
	}
	if err := writeJSONAtomic(filepath.Join(s.dir, fileIR), s.IR); err != nil {
		return err
	}
	return nil
}

// SaveDSL 把渲染出的 DSL 落 dsl.json（preview/upload 时调用）。
func (s *Session) SaveDSL(d *dsl.DSL) error {
	return writeJSONAtomic(filepath.Join(s.dir, fileDSL), d)
}

// Dir 返回会话目录（供命令回显路径）。
func (s *Session) Dir() string { return s.dir }

// AppCachePath 返回某 app schema 的缓存文件路径（M8 app-schema 用）。
func (s *Session) AppCachePath(appID string) string {
	return filepath.Join(s.dir, dirApp, appID+".json")
}

// writeJSONAtomic 原子写 JSON：写 .tmp 再 rename，避免半截文件。
func writeJSONAtomic(path string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal %s: %w", filepath.Base(path), err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", filepath.Base(path), err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("rename %s: %w", filepath.Base(path), err)
	}
	return nil
}
