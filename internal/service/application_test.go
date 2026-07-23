package service

import (
	"encoding/json"
	"testing"
)

// TestLikePattern 覆盖搜索词 → LIKE 模式：空串匹配全部，元字符转义防注入式通配。
func TestLikePattern(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"", "%"},
		{"   ", "%"},
		{"qwen", "%qwen%"},
		{"情感分类", "%情感分类%"},
		{"50%", `%50\%%`},       // % 转义
		{"a_b", `%a\_b%`},       // _ 转义
		{`x\y`, `%x\\y%`},       // \ 转义
	}
	for _, tc := range cases {
		if got := likePattern(tc.in); got != tc.want {
			t.Errorf("likePattern(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}


func TestNormalizeJSON(t *testing.T) {
	cases := []struct {
		name     string
		in       json.RawMessage
		wantErr  bool
		wantNull bool // 期望输出为 JSON 字面量 "null"（存 nullable 列避免 SQL NULL）
	}{
		{"empty", nil, false, true},
		{"literal null", json.RawMessage("null"), false, true},
		{"valid object", json.RawMessage(`{"a":1}`), false, false},
		{"valid array", json.RawMessage(`[1,2,3]`), false, false},
		{"invalid", json.RawMessage(`{bad`), true, false},
		{"trailing garbage", json.RawMessage(`{"a":1}x`), true, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, err := normalizeJSON(tc.in)
			if tc.wantErr && err == nil {
				t.Fatal("expected error, got nil")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !tc.wantErr {
				// 空/null 输入应归一为字面量 "null"（非 Go nil），使 nullable JSON 列
				// 存合法 JSON 而非 SQL NULL，避免读回时 json.RawMessage Scan 崩溃。
				if tc.wantNull && string(out) != "null" {
					t.Fatalf("expected literal null, got %q", out)
				}
				if !tc.wantNull && out == nil {
					t.Fatal("expected non-nil output")
				}
			}
		})
	}
}
