package varpool

import (
	"fmt"
	"strings"
	"sync"
)

// 系统输出变量：每个节点执行后自动可用，供失败分支引用（对齐 PaiFlow）。
const (
	SysErrorCode    = "errorCode"
	SysErrorMessage = "errorMessage"
)

// VariablePool 是工作流的运行时共享内存（设计文档 §7）。
// 两层映射：nodeID -> (outputName -> value)。并发安全。
// 它是一次运行的临时产物，不落库。
type VariablePool struct {
	mu   sync.RWMutex
	vars map[string]map[string]any
}

// New 创建空变量池。
func New() *VariablePool {
	return &VariablePool{vars: make(map[string]map[string]any)}
}

// Set 写入某节点某端口的输出值。
func (p *VariablePool) Set(nodeID, name string, val any) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.vars[nodeID] == nil {
		p.vars[nodeID] = make(map[string]any)
	}
	p.vars[nodeID][name] = val
}

// SetOutputs 批量写入某节点的所有输出。
func (p *VariablePool) SetOutputs(nodeID string, outputs map[string]any) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.vars[nodeID] == nil {
		p.vars[nodeID] = make(map[string]any)
	}
	for k, v := range outputs {
		p.vars[nodeID][k] = v
	}
}

// SetError 写入节点的系统错误变量，供失败分支/兜底引用。
func (p *VariablePool) SetError(nodeID string, code any, message string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.vars[nodeID] == nil {
		p.vars[nodeID] = make(map[string]any)
	}
	p.vars[nodeID][SysErrorCode] = code
	p.vars[nodeID][SysErrorMessage] = message
}

// Get 读取某节点输出。name 支持路径，如 "response.metadata.model"、
// "result.segments[0].text"：第一个字段是端口名，其余步进入其值。
func (p *VariablePool) Get(nodeID, name string) (any, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()

	inner, ok := p.vars[nodeID]
	if !ok {
		return nil, fmt.Errorf("node %q has no variables", nodeID)
	}

	tokens, err := parsePath(name)
	if err != nil {
		return nil, err
	}
	// 第一个 token 必须是字段（端口名）。
	if tokens[0].isIdx {
		return nil, fmt.Errorf("invalid ref %q: must start with a port name", name)
	}
	port := tokens[0].key
	root, exists := inner[port]
	if !exists {
		return nil, fmt.Errorf("node %q has no output %q", nodeID, port)
	}
	if len(tokens) == 1 {
		return root, nil
	}
	return walkPath(root, tokens[1:])
}

// Resolve 是 Get 的引用视图：接受 "nodeID" + "port[.path]"。
func (p *VariablePool) Resolve(nodeID, portPath string) (any, error) {
	return p.Get(nodeID, portPath)
}

// Has 判断某节点某端口是否存在（不解析路径）。
func (p *VariablePool) Has(nodeID, port string) bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	inner, ok := p.vars[nodeID]
	if !ok {
		return false
	}
	// port 可能带路径，只看第一段。
	head := port
	if i := strings.IndexAny(port, ".["); i >= 0 {
		head = port[:i]
	}
	_, exists := inner[head]
	return exists
}

// Snapshot 返回变量池的深拷贝，用于持久化 / 暂停恢复 / 调试。
func (p *VariablePool) Snapshot() map[string]map[string]any {
	p.mu.RLock()
	defer p.mu.RUnlock()
	out := make(map[string]map[string]any, len(p.vars))
	for node, inner := range p.vars {
		cp := make(map[string]any, len(inner))
		for k, v := range inner {
			cp[k] = v
		}
		out[node] = cp
	}
	return out
}
