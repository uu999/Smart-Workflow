package nodes

import "context"

// AppInfo 是 application 节点执行所需的应用元数据（由 AppResolver 从存储解析）。
// kind 决定 ApplicationExecutor 委托给哪个底层执行器（http/python/rpc）；
// config 是应用的调用配置（endpoint/headers/code/timeout 等，随 kind 语义不同）。
type AppInfo struct {
	AppID  string
	Kind   string         // http / python / rpc
	Name   string
	Config map[string]any // 应用调用配置（http: url/headers；python: code 等）
}

// AppResolver 按 app_id 解析应用元数据。engine 装配时用 store 适配器实现，
// 注入到 nodes.Config —— 执行器不直接依赖 DB，沿用 CodeExecutor{SidecarURL} 的 DI 范式。
type AppResolver interface {
	ResolveApp(ctx context.Context, appID string) (*AppInfo, error)
}

// DatasetResolver 按 dataset_id 解析评测集行数据（JSON 数组的每个元素为一条样本）。
type DatasetResolver interface {
	ResolveDataset(ctx context.Context, datasetID string) ([]map[string]any, error)
}
