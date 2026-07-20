package service

import (
	"context"
	"testing"
)

// resolveVersion 的 -1（草稿）与 >0（指定版本）分支在触库前 early-return，
// 可零 DB 覆盖版本语义这一文档化行为；0/缺省分支触库，留给集成测试覆盖。
func TestResolveVersion_NoDBBranches(t *testing.T) {
	s := &RunService{} // 不触库的分支无需 store。
	cases := []struct {
		name string
		in   int32
		want int32
	}{
		{"draft", DraftVersion, DraftVersion},
		{"explicit v1", 1, 1},
		{"explicit v7", 7, 7},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := s.resolveVersion(context.Background(), "wf_x", tc.in)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Fatalf("resolveVersion(%d) = %d, want %d", tc.in, got, tc.want)
			}
		})
	}
}
