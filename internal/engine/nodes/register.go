package nodes

// Config 是构造内置执行器所需的配置（改造②：config 打通）。
// 目前只有 code 节点需要 sidecar 地址；后续 llm 等节点的配置也从这里注入。
type Config struct {
	SidecarURL string
}

// NewDefaultRegistry 按配置构造一个装好内置执行器的注册表。
// 改造①②：取代原先包级 init() 的全局副作用注册——
// code 节点的 sidecar 地址在此从 config 注入，不再依赖环境变量兜底。
func NewDefaultRegistry(cfg Config) *Registry {
	r := NewRegistry()
	r.Register(StartExecutor{})
	r.Register(EndExecutor{})
	r.Register(ConditionExecutor{})
	r.Register(HTTPExecutor{})
	r.Register(CodeExecutor{SidecarURL: cfg.SidecarURL})
	return r
}
