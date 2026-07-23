package nodes

import (
	"context"
	"fmt"

	"github.com/smart-workflow/smart-workflow/internal/dsl"
)

// ApplicationExecutor 是 application 节点执行器（M10）：按节点 app_id 解析应用元数据，
// 依 kind 委托给既有底层执行器——不新造远程调用层（承重决策，见 plan.md 纠正1）：
//   - kind=http   → 复用 HTTPExecutor（config 提供 url/method/headers）
//   - kind=python → 复用 CodeExecutor（config 提供 code）
//   - kind=rpc    → 暂明确「未支持」（无 RPC 传输层）
//
// 委托方式：用 AppInfo.Config 合成一个「配置就位」的节点交给底层执行器，
// 保留原节点的 Inputs（端口绑定）——底层执行器读 nodeParam 的姿势不变。
//
// 完整委托与测试见 #29；本文件建立结构与分派骨架。
type ApplicationExecutor struct {
	Resolver AppResolver
	Code     CodeExecutor
	HTTP     HTTPExecutor
}

func (ApplicationExecutor) Type() string { return dsl.KindApplication }

func (e ApplicationExecutor) Execute(ctx context.Context, ec *ExecContext) (*NodeResult, error) {
	if e.Resolver == nil {
		return nil, fmt.Errorf("application node %s: no app resolver configured", ec.Node.ID)
	}
	appID := appIDOf(ec.Node)
	if appID == "" {
		return nil, fmt.Errorf("application node %s: missing app_id", ec.Node.ID)
	}
	info, err := e.Resolver.ResolveApp(ctx, appID)
	if err != nil {
		return nil, fmt.Errorf("application node %s: resolve %s: %w", ec.Node.ID, appID, err)
	}

	switch info.Kind {
	case "http":
		return e.HTTP.Execute(ctx, e.delegateContext(ec, info.Config))
	case "python":
		return e.Code.Execute(ctx, e.delegateContext(ec, info.Config))
	case "rpc":
		return nil, fmt.Errorf("application node %s: kind=rpc not supported yet (no rpc transport)", ec.Node.ID)
	default:
		return nil, fmt.Errorf("application node %s: unknown app kind %q", ec.Node.ID, info.Kind)
	}
}

// delegateContext 用应用 config 合成底层执行器所需的 nodeParam（保留原 Inputs）。
// config 的键（url/method/headers/code/timeout）直接进 nodeParam，底层执行器照常读。
func (e ApplicationExecutor) delegateContext(ec *ExecContext, config map[string]any) *ExecContext {
	merged := make(map[string]any, len(ec.Node.Data.NodeParam)+len(config))
	for k, v := range ec.Node.Data.NodeParam {
		merged[k] = v
	}
	for k, v := range config {
		merged[k] = v // 应用 config 覆盖同名 nodeParam（config 是权威调用配置）
	}
	node := ec.Node
	node.Data.NodeParam = merged
	return &ExecContext{Node: node, Inputs: ec.Inputs, Pool: ec.Pool, RunID: ec.RunID}
}

// appIDOf 从 DSL 节点取 app_id（render 物化到 nodeParam.appId）。
func appIDOf(n dsl.DSLNode) string {
	p := n.Data.NodeParam
	if v, ok := p["appId"].(string); ok && v != "" {
		return v
	}
	if v, ok := p["app_id"].(string); ok && v != "" {
		return v
	}
	return ""
}
