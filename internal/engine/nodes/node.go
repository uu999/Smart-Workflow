package nodes

import (
	"context"
	"sync"

	"github.com/smart-workflow/smart-workflow/internal/dsl"
	"github.com/smart-workflow/smart-workflow/internal/engine/varpool"
)

// 节点运行状态。
const (
	StatusPending   = "pending"
	StatusRunning   = "running"
	StatusSucceeded = "succeeded"
	StatusFailed    = "failed"
	StatusSkipped   = "skipped"
)

// ExecContext 是节点执行的上下文。Inputs 是已解析好的输入值。
type ExecContext struct {
	Node   dsl.DSLNode
	Inputs map[string]any
	Pool   *varpool.VariablePool
	RunID  string
}

// NodeResult 是节点执行结果。
// Outputs 写入变量池；Branch 供 condition 节点指示命中的分支句柄。
type NodeResult struct {
	Outputs map[string]any
	Branch  string // condition 命中分支序号；其余节点为空
}

// NodeExecutor 是所有节点类型的统一执行接口。
type NodeExecutor interface {
	Type() string
	Execute(ctx context.Context, ec *ExecContext) (*NodeResult, error)
}

// Registry 是节点类型 -> 执行器的注册表。
// 改造①②：由包级全局单例改为实例化类型，
//   - 支持按 config 注入不同配置的执行器（如 code 节点的 sidecar 地址）；
//   - 带 RWMutex，Register/Get 并发安全，可用于 -race 与并行测试。
//
// 由引擎实例持有，随一次运行的生命周期存在，不再是进程级全局状态。
type Registry struct {
	mu   sync.RWMutex
	byID map[string]NodeExecutor
}

// NewRegistry 返回空注册表。
func NewRegistry() *Registry {
	return &Registry{byID: make(map[string]NodeExecutor)}
}

// Register 注册（或覆盖）一个节点执行器。
func (r *Registry) Register(e NodeExecutor) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.byID[e.Type()] = e
}

// Get 按类型取执行器，不存在返回 nil,false。
func (r *Registry) Get(nodeType string) (NodeExecutor, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	e, ok := r.byID[nodeType]
	return e, ok
}

// Types 返回已注册的节点类型集合（供校验 / 能力发现）。
func (r *Registry) Types() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]string, 0, len(r.byID))
	for k := range r.byID {
		out = append(out, k)
	}
	return out
}
