package service

import (
	"encoding/json"
	"errors"
	"testing"
)

// TestNormalizeRows 覆盖 dataset 行集归一：空/null → []（0 行），合法数组算行数，
// 非数组（对象/标量/坏 JSON）报 ErrDatasetRowsNotArray。
func TestNormalizeRows(t *testing.T) {
	cases := []struct {
		name     string
		in       json.RawMessage
		wantErr  bool
		wantN    int32
		wantData string // 仅在无错时校验归一化字节
	}{
		{"empty", nil, false, 0, "[]"},
		{"literal null", json.RawMessage("null"), false, 0, "[]"},
		{"empty array", json.RawMessage(`[]`), false, 0, `[]`},
		{"two rows", json.RawMessage(`[{"q":"a"},{"q":"b"}]`), false, 2, `[{"q":"a"},{"q":"b"}]`},
		{"scalar rows", json.RawMessage(`[1,2,3]`), false, 3, `[1,2,3]`},
		{"object not array", json.RawMessage(`{"q":"a"}`), true, 0, ""},
		{"scalar not array", json.RawMessage(`42`), true, 0, ""},
		{"bad json", json.RawMessage(`[{bad`), true, 0, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			data, n, err := normalizeRows(tc.in)
			if tc.wantErr {
				if !errors.Is(err, ErrDatasetRowsNotArray) {
					t.Fatalf("expected ErrDatasetRowsNotArray, got %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if n != tc.wantN {
				t.Errorf("row count = %d, want %d", n, tc.wantN)
			}
			if string(data) != tc.wantData {
				t.Errorf("data = %s, want %s", data, tc.wantData)
			}
		})
	}
}
