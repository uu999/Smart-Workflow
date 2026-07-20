package service

import (
	"context"
	"errors"
	"testing"

	"github.com/smart-workflow/smart-workflow/internal/dsl"
	"github.com/smart-workflow/smart-workflow/internal/validator"
)

// ptrI32 是取址 helper，用于构造 *int32 测试入参。
func ptrI32(v int32) *int32 { return &v }

// resolveVersion 的 -1（草稿）与 >0（指定版本）分支在触库前 early-return，
// 显式非法值（0 / < -1）返回 ErrInvalidVersion，均可零 DB 覆盖版本语义；
// nil（省略→取已发布）分支触库，留给集成测试覆盖。
func TestResolveVersion_NoDBBranches(t *testing.T) {
	s := &RunService{} // 不触库的分支无需 store。
	cases := []struct {
		name    string
		in      *int32
		want    int32
		wantErr error
	}{
		{"draft", ptrI32(DraftVersion), DraftVersion, nil},
		{"explicit v1", ptrI32(1), 1, nil},
		{"explicit v7", ptrI32(7), 7, nil},
		{"explicit zero is invalid", ptrI32(0), 0, ErrInvalidVersion},
		{"below draft is invalid", ptrI32(-2), 0, ErrInvalidVersion},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := s.resolveVersion(context.Background(), "wf_x", tc.in)
			if tc.wantErr != nil {
				if err != tc.wantErr {
					t.Fatalf("resolveVersion err = %v, want %v", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Fatalf("resolveVersion(%v) = %d, want %d", tc.in, got, tc.want)
			}
		})
	}
}

// TestValidationError_CarriesIssues 验证 TD-10 gate 的错误类型：
// 携带结构化 issues、errors.As 可提取、消息含 error 计数。
func TestValidationError_CarriesIssues(t *testing.T) {
	verr := &ValidationError{Issues: []validator.Issue{
		{Code: "start_count", Severity: validator.SeverityError, Message: "no start"},
		{Code: "unreachable_node", Severity: validator.SeverityWarning, Message: "orphan"},
	}}
	// errors.As 能从包裹链里提取（helpers.failFromErr 依赖此行为）。
	var target *ValidationError
	if !errors.As(error(verr), &target) {
		t.Fatal("errors.As failed to extract *ValidationError")
	}
	if len(target.Issues) != 2 {
		t.Fatalf("want 2 issues, got %d", len(target.Issues))
	}
	// 消息只计 error 级（1 条），不含 warning。
	if got := verr.Error(); got != "workflow validation failed with 1 error(s); fix them before running" {
		t.Fatalf("unexpected message: %q", got)
	}
}

// TestValidateDSL_BadGraphHasError 验证 gate 依赖的 validateDSL 对坏图
// （缺 start、坏 ref）确实报 error，是 gate 拒绝 run 的判据来源。
func TestValidateDSL_BadGraphHasError(t *testing.T) {
	// 只有一个 end、无 start，且 end 引用不存在的上游端口。
	d := &dsl.DSL{
		Nodes: []dsl.DSLNode{
			{
				ID: "end::1",
				Data: dsl.NodeData{
					NodeMeta: dsl.NodeMeta{NodeType: dsl.KindEnd},
					Inputs: []dsl.InputItem{{
						Name: "x",
						Schema: dsl.InputSchema{Value: dsl.Value{
							Type: dsl.DSLValueRef, Content: dsl.RefContent{NodeID: "ghost::1", Name: "y"},
						}},
					}},
				},
			},
		},
	}
	res := validateDSL(d, dsl.Meta{})
	if !res.HasError() {
		t.Fatalf("bad graph should have error-level issues, got %+v", res.Issues)
	}
}
