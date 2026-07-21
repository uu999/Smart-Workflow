package catalog

import (
	"encoding/json"
	"testing"

	"github.com/smart-workflow/smart-workflow/internal/dsl"
)

func TestParsePortList(t *testing.T) {
	t.Run("nil and null -> nil", func(t *testing.T) {
		for _, raw := range []json.RawMessage{nil, json.RawMessage("null"), json.RawMessage("")} {
			ports, err := ParsePortList(raw)
			if err != nil || ports != nil {
				t.Fatalf("raw=%q: ports=%v err=%v, want nil,nil", raw, ports, err)
			}
		}
	})

	t.Run("valid port array", func(t *testing.T) {
		raw := json.RawMessage(`[
			{"name":"text","type":"string","required":true,"desc":"待分类文本"},
			{"name":"topk","type":"integer"}
		]`)
		ports, err := ParsePortList(raw)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(ports) != 2 {
			t.Fatalf("want 2 ports, got %d", len(ports))
		}
		if ports[0] != (dsl.Port{Name: "text", Type: "string", Required: true}) {
			t.Errorf("port[0] = %+v", ports[0])
		}
		if ports[1] != (dsl.Port{Name: "topk", Type: "integer"}) {
			t.Errorf("port[1] = %+v", ports[1])
		}
	})

	t.Run("missing name/type -> error", func(t *testing.T) {
		for _, raw := range []string{
			`[{"type":"string"}]`,
			`[{"name":"x"}]`,
		} {
			if _, err := ParsePortList(json.RawMessage(raw)); err == nil {
				t.Errorf("raw=%s should error", raw)
			}
		}
	})

	t.Run("not an array -> error", func(t *testing.T) {
		if _, err := ParsePortList(json.RawMessage(`{"properties":{}}`)); err == nil {
			t.Error("object schema should error (we use port-array format)")
		}
	})
}

func TestParseAppSchema(t *testing.T) {
	in := json.RawMessage(`[{"name":"text","type":"string","required":true}]`)
	out := json.RawMessage(`[{"name":"label","type":"string"},{"name":"confidence","type":"number"}]`)
	sch, err := ParseAppSchema("app_1", "情感分类器", in, out)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if sch.AppID != "app_1" || sch.Name != "情感分类器" {
		t.Errorf("meta lost: %+v", sch)
	}
	if len(sch.Inputs) != 1 || len(sch.Outputs) != 2 {
		t.Fatalf("ports: in=%d out=%d", len(sch.Inputs), len(sch.Outputs))
	}
	if !sch.Inputs[0].Required {
		t.Error("input required flag lost")
	}
}

func TestParseAppSchema_BadInputSurfaced(t *testing.T) {
	_, err := ParseAppSchema("app_1", "x", json.RawMessage(`{bad`), nil)
	if err == nil {
		t.Fatal("bad input_schema should surface error")
	}
}
