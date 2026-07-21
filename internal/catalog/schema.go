// Package catalog 实现 M8 能力发现的 schema 契约与解析。
//
// application 的 input_schema / output_schema 采用「端口数组」格式（对齐 §2.1
// app-schema 语义）：一个端口声明列表，每项 {name, type, required, desc}。
//
//	[
//	  {"name": "text", "type": "string", "required": true, "desc": "待分类文本"},
//	  {"name": "topk", "type": "integer"}
//	]
//
// 设计取舍（参考 Byteval workflow builder 的 ir.json 端口列表，但采用 Smart-Workflow
// 既有的字符串 type 而非 PaiFlow 的整数 value_type）：
//   - 端口数组与 dsl.Port 一一对应，app-schema 解析零歧义、无损映射；
//   - 沿用字符串 type，与 validator/render/scope 的类型比对保持同一套词汇；
//   - 若某 app 的 schema 为空/nil，返回空端口列表（半成品友好，交给 validate 兜底）。
package catalog

import (
	"encoding/json"
	"fmt"

	"github.com/smart-workflow/smart-workflow/internal/dsl"
)

// PortSpec 是端口数组里的一项，即 app-schema 的最小契约单元。
type PortSpec struct {
	Name     string `json:"name"`
	Type     string `json:"type"`
	Required bool   `json:"required,omitempty"`
	Desc     string `json:"desc,omitempty"`
}

// ParsePortList 把 application 的 input_schema/output_schema（端口数组 JSON）
// 解析成 []dsl.Port。空/nil 输入返回 nil（不是错误）；非数组或缺 name/type 报错。
func ParsePortList(raw json.RawMessage) ([]dsl.Port, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	var specs []PortSpec
	if err := json.Unmarshal(raw, &specs); err != nil {
		return nil, fmt.Errorf("schema is not a port array: %w", err)
	}
	ports := make([]dsl.Port, 0, len(specs))
	for i, s := range specs {
		if s.Name == "" {
			return nil, fmt.Errorf("port[%d] missing name", i)
		}
		if s.Type == "" {
			return nil, fmt.Errorf("port[%d] (%s) missing type", i, s.Name)
		}
		ports = append(ports, dsl.Port{Name: s.Name, Type: s.Type, Required: s.Required})
	}
	return ports, nil
}

// AppSchema 是 app-schema 命令解析出的一个应用契约（输入/输出端口）。
type AppSchema struct {
	AppID   string     `json:"app_id"`
	Name    string     `json:"name"`
	Inputs  []dsl.Port `json:"inputs"`
	Outputs []dsl.Port `json:"outputs"`
}

// ParseAppSchema 把应用的 input/output schema 原文解析成 AppSchema。
func ParseAppSchema(appID, name string, inputSchema, outputSchema json.RawMessage) (*AppSchema, error) {
	in, err := ParsePortList(inputSchema)
	if err != nil {
		return nil, fmt.Errorf("input_schema: %w", err)
	}
	out, err := ParsePortList(outputSchema)
	if err != nil {
		return nil, fmt.Errorf("output_schema: %w", err)
	}
	return &AppSchema{AppID: appID, Name: name, Inputs: in, Outputs: out}, nil
}
