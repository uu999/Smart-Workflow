package validator

import (
	"testing"

	"github.com/smart-workflow/smart-workflow/internal/dsl"
)

// validIR 是一条通过校验的最小工作流 start -> app -> end。
func validIR() *dsl.IR {
	return &dsl.IR{
		Nodes: []dsl.Node{
			{ID: "start_0", Kind: dsl.KindStart, Outputs: []dsl.Port{{Name: "query", Type: dsl.ValueTypeString}}},
			{
				ID: "app_1", Kind: dsl.KindApplication, AppID: "1",
				Inputs:  []dsl.Port{{Name: "text", Type: dsl.ValueTypeString, Required: true}},
				Outputs: []dsl.Port{{Name: "label", Type: dsl.ValueTypeString}},
				Bindings: []dsl.Binding{
					{Port: "text", Mode: dsl.BindModeRef, SourceNode: "start_0", SourcePort: "query"},
				},
			},
			{
				ID: "end_0", Kind: dsl.KindEnd,
				Inputs: []dsl.Port{{Name: "out", Type: dsl.ValueTypeString}},
				Bindings: []dsl.Binding{
					{Port: "out", Mode: dsl.BindModeRef, SourceNode: "app_1", SourcePort: "label"},
				},
			},
		},
		Edges: []dsl.Edge{
			{Source: "start_0", Target: "app_1"},
			{Source: "app_1", Target: "end_0"},
		},
	}
}

func hasCode(r *Result, code string) bool {
	for _, i := range r.Issues {
		if i.Code == code {
			return true
		}
	}
	return false
}

func TestValidate_ValidPasses(t *testing.T) {
	r := Validate(validIR())
	if r.HasError() {
		t.Errorf("valid IR should have no error, got %+v", r.Issues)
	}
}

func TestValidate_StartCount(t *testing.T) {
	ir := validIR()
	ir.Nodes[0].Kind = dsl.KindApplication // 干掉唯一的 start
	ir.Nodes[0].AppID = "x"
	r := Validate(ir)
	if !hasCode(r, "start_count") {
		t.Errorf("expected start_count, got %+v", r.Issues)
	}
}

func TestValidate_EndMissing(t *testing.T) {
	ir := validIR()
	ir.Nodes[2].Kind = dsl.KindApplication
	ir.Nodes[2].AppID = "y"
	r := Validate(ir)
	if !hasCode(r, "end_missing") {
		t.Errorf("expected end_missing, got %+v", r.Issues)
	}
}

func TestValidate_DuplicateID(t *testing.T) {
	ir := validIR()
	ir.Nodes[1].ID = "start_0"
	r := Validate(ir)
	if !hasCode(r, "duplicate_node_id") {
		t.Errorf("expected duplicate_node_id, got %+v", r.Issues)
	}
}

func TestValidate_UnsupportedKind(t *testing.T) {
	ir := validIR()
	ir.Nodes[1].Kind = "magic"
	r := Validate(ir)
	if !hasCode(r, "unsupported_node_type") {
		t.Errorf("expected unsupported_node_type, got %+v", r.Issues)
	}
}

func TestValidate_FieldNotAllowedForKind(t *testing.T) {
	ir := validIR()
	ir.Nodes[0].AppID = "should-not-be-here" // start 不该有 app_id
	r := Validate(ir)
	if !hasCode(r, "field_not_allowed_for_kind") {
		t.Errorf("expected field_not_allowed_for_kind, got %+v", r.Issues)
	}
}

func TestValidate_BadEdge(t *testing.T) {
	ir := validIR()
	ir.Edges = append(ir.Edges, dsl.Edge{Source: "ghost", Target: "end_0"})
	r := Validate(ir)
	if !hasCode(r, "bad_edge") {
		t.Errorf("expected bad_edge, got %+v", r.Issues)
	}
}

func TestValidate_Cycle(t *testing.T) {
	ir := validIR()
	ir.Edges = append(ir.Edges, dsl.Edge{Source: "end_0", Target: "app_1"}) // 制造环
	r := Validate(ir)
	if !hasCode(r, "cycle") {
		t.Errorf("expected cycle, got %+v", r.Issues)
	}
}

func TestValidate_RequiredNotBound(t *testing.T) {
	ir := validIR()
	ir.Nodes[1].Bindings = nil // app.text 必填但不绑
	r := Validate(ir)
	if !hasCode(r, "required_not_bound") {
		t.Errorf("expected required_not_bound, got %+v", r.Issues)
	}
}

func TestValidate_SourcePortNotFound(t *testing.T) {
	ir := validIR()
	ir.Nodes[1].Bindings[0].SourcePort = "nonexistent"
	r := Validate(ir)
	if !hasCode(r, "source_port_not_found") {
		t.Errorf("expected source_port_not_found, got %+v", r.Issues)
	}
}

func TestValidate_RefNodeNotFound(t *testing.T) {
	ir := validIR()
	ir.Nodes[1].Bindings[0].SourceNode = "ghost"
	r := Validate(ir)
	if !hasCode(r, "ref_node_not_found") {
		t.Errorf("expected ref_node_not_found, got %+v", r.Issues)
	}
}

func TestValidate_TypeMismatch(t *testing.T) {
	ir := validIR()
	ir.Nodes[1].Inputs[0].Type = dsl.ValueTypeInteger // text 声明成 integer，但引用的是 string
	r := Validate(ir)
	if !hasCode(r, "type_mismatch") {
		t.Errorf("expected type_mismatch, got %+v", r.Issues)
	}
}

func TestValidate_CrossScopeForbidden(t *testing.T) {
	ir := validIR()
	ir.Nodes[1].Bindings[0].Scope = "parent"
	r := Validate(ir)
	if !hasCode(r, "cross_scope_ref_forbidden") {
		t.Errorf("expected cross_scope_ref_forbidden, got %+v", r.Issues)
	}
}

func TestValidate_UnreachableWarning(t *testing.T) {
	ir := validIR()
	// 加一个孤立 application 节点，无边连入。
	ir.Nodes = append(ir.Nodes, dsl.Node{ID: "orphan", Kind: dsl.KindApplication, AppID: "z"})
	r := Validate(ir)
	if !hasCode(r, "unreachable_node") {
		t.Errorf("expected unreachable_node warning, got %+v", r.Issues)
	}
}
