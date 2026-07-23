package nodes

// Config 是构造内置执行器所需的配置（改造②：config 打通）。
// code 节点需要 sidecar 地址；application/dataset 节点需要 store 解析器（M10，DI 注入）。
type Config struct {
	SidecarURL string
	// AppResolver/DatasetResolver 为 nil 时不注册对应执行器（application/dataset）——
	// 保持无 store 的单测/进程零影响；engine.New 装配时才注入真实解析器。
	AppResolver     AppResolver
	DatasetResolver DatasetResolver
}

// NewDefaultRegistry 按配置构造一个装好内置执行器的注册表。
// 改造①②：取代原先包级 init() 的全局副作用注册——
// code 节点的 sidecar 地址在此从 config 注入，不再依赖环境变量兜底。
//
// M10：仅当注入了对应 resolver 时才注册 application/dataset 执行器；
// 未注入时这两类节点会在调度期报 "no executor registered"（与既有行为一致）。
func NewDefaultRegistry(cfg Config) *Registry {
	r := NewRegistry()
	r.Register(StartExecutor{})
	r.Register(EndExecutor{})
	r.Register(ConditionExecutor{})
	r.Register(HTTPExecutor{})
	code := CodeExecutor{SidecarURL: cfg.SidecarURL}
	r.Register(code)
	if cfg.AppResolver != nil {
		// application 节点按 kind 委托 http/code，故复用同一 code 执行器（python kind）。
		r.Register(ApplicationExecutor{Resolver: cfg.AppResolver, Code: code, HTTP: HTTPExecutor{}})
	}
	if cfg.DatasetResolver != nil {
		r.Register(DatasetExecutor{Resolver: cfg.DatasetResolver})
	}
	return r
}
