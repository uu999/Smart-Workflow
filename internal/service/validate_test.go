package service

import (
	"testing"

	"github.com/smart-workflow/smart-workflow/internal/dsl"
)

// validateDSL 是纯逻辑（DSL→IR→Validate），无需 DB。
func TestValidateDSL_EmptyReportsMissingStartEnd(t *testing.T) {
	result := validateDSL(&dsl.DSL{}, dsl.Meta{})
	if result == nil {
		t.Fatal("nil result")
	}
	// 空图应有 error 级问题（缺 start/end）。
	if !result.HasError() {
		t.Fatalf("empty DSL should produce errors, got %+v", result.Issues)
	}
}

func TestValidateDSL_ValidGraphPasses(t *testing.T) {
	d := &dsl.DSL{
		Nodes: []dsl.DSLNode{
			{ID: "start::1", Data: dsl.NodeData{
				NodeMeta: dsl.NodeMeta{NodeType: dsl.KindStart},
				Outputs:  []dsl.OutputItem{{ID: "o-q", Name: "query", Schema: map[string]any{"type": "string"}}},
			}},
			{ID: "end::1", Data: dsl.NodeData{
				NodeMeta: dsl.NodeMeta{NodeType: dsl.KindEnd},
			}},
		},
		Edges: []dsl.DSLEdge{{SourceNodeID: "start::1", TargetNodeID: "end::1"}},
	}
	result := validateDSL(d, dsl.Meta{Name: "t"})
	if result.HasError() {
		t.Fatalf("valid graph should have no errors, got %+v", result.Issues)
	}
}
