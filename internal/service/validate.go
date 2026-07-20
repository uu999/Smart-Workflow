package service

import (
	"context"

	"github.com/smart-workflow/smart-workflow/internal/dsl"
	"github.com/smart-workflow/smart-workflow/internal/storage/mysql"
	"github.com/smart-workflow/smart-workflow/internal/validator"
)

// ValidateService 对存储的工作流做静态校验（M6）。
// 校验器吃编辑态 IR，故先把存储的 DSL 反渲染成 IR 再校验。
type ValidateService struct {
	store *mysql.Store
	wf    *WorkflowService
}

func NewValidateService(store *mysql.Store) *ValidateService {
	return &ValidateService{store: store, wf: NewWorkflowService(store)}
}

// ValidateDraft 校验工作流草稿 DSL，返回结构化问题清单。
func (s *ValidateService) ValidateDraft(ctx context.Context, workflowID string) (*validator.Result, error) {
	wf, err := s.wf.Get(ctx, workflowID)
	if err != nil {
		return nil, err
	}
	return validateDSL(wf.Draft, dsl.Meta{Name: wf.Name, ProjectID: wf.ProjectID}), nil
}

// ValidateVersion 校验某个已发布版本的 DSL。
func (s *ValidateService) ValidateVersion(ctx context.Context, workflowID string, version int32) (*validator.Result, error) {
	d, err := s.wf.GetVersion(ctx, workflowID, version)
	if err != nil {
		return nil, err
	}
	return validateDSL(d, dsl.Meta{}), nil
}

// validateDSL 把 DSL 反渲染成 IR 并校验；ToIR 失败也归一成一条 error 级问题，
// 让调用方始终拿到结构化结果而非裸 error。
func validateDSL(d *dsl.DSL, meta dsl.Meta) *validator.Result {
	if d == nil {
		d = &dsl.DSL{}
	}
	ir, err := dsl.ToIR(d, meta)
	if err != nil {
		return &validator.Result{Issues: []validator.Issue{{
			Code:     "dsl_to_ir_failed",
			Severity: validator.SeverityError,
			Message:  err.Error(),
		}}}
	}
	return validator.Validate(ir)
}
