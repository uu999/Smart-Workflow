package service

import (
	"encoding/json"
	"testing"
)

func TestNormalizeJSON(t *testing.T) {
	cases := []struct {
		name    string
		in      json.RawMessage
		wantErr bool
		wantNil bool
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
				if tc.wantNil && out != nil {
					t.Fatalf("expected nil, got %s", out)
				}
				if !tc.wantNil && out == nil {
					t.Fatal("expected non-nil output")
				}
			}
		})
	}
}
