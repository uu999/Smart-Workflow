package scheduler

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/smart-workflow/smart-workflow/internal/dsl"
	"github.com/smart-workflow/smart-workflow/internal/engine/builder"
	"github.com/smart-workflow/smart-workflow/internal/engine/nodes"
	"github.com/smart-workflow/smart-workflow/internal/engine/varpool"
	"github.com/smart-workflow/smart-workflow/internal/runevent"
)

// Options 配置一次调度执行。
type Options struct {
	RunID       string
	Input       map[string]any    // 注入 start 节点的运行入参
	Concurrency int               // 并发上限，<=0 时取 8
	Registry    *nodes.Registry   // 节点执行器注册表（改造①②：由调用方注入，不再用包级全局）
	Emitter     runevent.Emitter  // M9-b：节点事件发射器，nil=不发（零开销）
}

// NodeExecInfo 是单节点执行记录，供 engine.Run 落 node_run。
type NodeExecInfo struct {
	NodeID     string
	NodeType   string
	Status     string
	Input      map[string]any
	Output     map[string]any
	Err        string
	Attempt    int
	Cost       time.Duration
	StartedAt  time.Time
	FinishedAt time.Time
}

// Result 是一次调度的整体结果。
type Result struct {
	Outputs map[string]any // 合并所有 End 节点的输出
	Nodes   []NodeExecInfo // 每个节点的执行快照（含 skipped）
}

// Run 执行一个 Plan：依赖驱动就绪队列 + goroutine 并发 + retry + condition 剪枝。
// 任一节点失败按中断策略终止整图（ErrStrategyInterrupted）。
func Run(ctx context.Context, plan *builder.Plan, pool *varpool.VariablePool, opts Options) (*Result, error) {
	if opts.Concurrency <= 0 {
		opts.Concurrency = 8
	}
	if opts.Registry == nil {
		return nil, fmt.Errorf("scheduler: nil registry")
	}
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	c := &coord{
		plan:        plan,
		pool:        pool,
		registry:    opts.Registry,
		runInput:    opts.Input,
		runID:       opts.RunID,
		concurrency: opts.Concurrency,
		cancel:      cancel,
		emitter:     opts.Emitter,
		states:      make(map[string]*nodeState, len(plan.Nodes)),
	}
	if c.emitter == nil {
		c.emitter = runevent.NopEmitter{}
	}
	for id := range plan.Nodes {
		c.states[id] = &nodeState{
			status:    nodes.StatusPending,
			totalIn:   len(plan.InEdges[id]),
			unsettled: len(plan.InEdges[id]),
		}
	}
	for _, edges := range plan.InEdges {
		for _, e := range edges {
			if c.isBranchEdge(e) {
				c.states[e.TargetNodeID].branchIn++
			}
		}
	}

	err := c.loop(runCtx)
	return c.result(), err
}

type nodeState struct {
	status     string
	result     *nodes.NodeResult
	err        error
	input      map[string]any
	cost       time.Duration
	attempt    int
	startedAt  time.Time
	finishedAt time.Time
	totalIn    int
	unsettled  int
	liveIn     int
	branchIn   int
	liveBranch int
	decided    bool // 已决定运行或跳过，防重复路由
}

type event struct {
	id         string
	status     string
	result     *nodes.NodeResult
	err        error
	input      map[string]any
	cost       time.Duration
	attempt    int
	startedAt  time.Time
	finishedAt time.Time
}

type coord struct {
	plan        *builder.Plan
	pool        *varpool.VariablePool
	registry    *nodes.Registry
	runInput    map[string]any
	runID       string
	concurrency int
	cancel      context.CancelFunc
	emitter     runevent.Emitter
	seq         int64 // 单调事件序号，仅在单线程 loop 内自增，无需锁

	states   map[string]*nodeState
	inFlight int
	abortErr error
}

// emit 在单线程 loop 内发一条事件，分配单调 seq（保证有序）。
func (c *coord) emit(phase, nodeID, nodeType, status, errMsg string, out map[string]any, costMs int64) {
	c.seq++
	c.emitter.Emit(runevent.RunEvent{
		RunID:    c.runID,
		Seq:      c.seq,
		Phase:    phase,
		NodeID:   nodeID,
		NodeType: nodeType,
		Status:   status,
		Output:   out,
		Error:    errMsg,
		CostMs:   costMs,
		TS:       runevent.NowMillis(),
	})
}

// loop 是单线程协调器：拥有全部 states/ready/计数，workers 仅回传 event。
func (c *coord) loop(ctx context.Context) error {
	done := make(chan event, c.concurrency)
	var ready []string

	// 种子：无入边的根节点（start / 孤立节点）直接就绪运行。
	for id, s := range c.states {
		if s.totalIn == 0 {
			s.decided = true
			ready = append(ready, id)
		}
	}

	for {
		for len(ready) > 0 && c.inFlight < c.concurrency && c.abortErr == nil {
			id := ready[0]
			ready = ready[1:]
			c.inFlight++
			// node_start：真正派发执行前发（单线程内，seq 有序）。
			c.emit(runevent.PhaseNodeStart, id, c.nodeType(id), nodes.StatusRunning, "", nil, 0)
			go c.execNode(ctx, id, done)
		}
		// 无在途：正常跑完（ready 必空），或 abort 后已排空。
		if c.inFlight == 0 {
			break
		}

		e := <-done
		c.inFlight--

		st := c.states[e.id]
		st.status = e.status
		st.result = e.result
		st.err = e.err
		st.input = e.input
		st.cost = e.cost
		st.attempt = e.attempt
		st.startedAt = e.startedAt
		st.finishedAt = e.finishedAt

		// node_end：节点终态事件（succeeded/failed）。
		var out map[string]any
		if e.result != nil {
			out = e.result.Outputs
		}
		errMsg := ""
		if e.err != nil {
			errMsg = e.err.Error()
		}
		c.emit(runevent.PhaseNodeEnd, e.id, c.nodeType(e.id), e.status, errMsg, out, e.cost.Milliseconds())

		if e.status == nodes.StatusFailed {
			if c.abortErr == nil {
				c.abortErr = fmt.Errorf("node %s failed: %w", e.id, e.err)
			}
			c.cancel() // 通知在途 worker 取消；停止派发新节点，只等在途排空
			continue
		}
		if c.abortErr == nil {
			c.propagate(e.id, &ready)
		}
	}
	return c.abortErr
}

// nodeType 返回节点类型（发事件用）。
func (c *coord) nodeType(id string) string {
	return c.plan.Nodes[id].Data.NodeMeta.NodeType
}

// propagate 结算已终态节点的出边，级联跳过全死入边的下游，返回新就绪运行的节点。
func (c *coord) propagate(fromID string, ready *[]string) {
	queue := []string{fromID}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]

		curSt := c.states[cur]
		succeeded := curSt.status == nodes.StatusSucceeded
		isCond := c.plan.Nodes[cur].Data.NodeMeta.NodeType == dsl.KindCondition
		branch := ""
		if curSt.result != nil {
			branch = curSt.result.Branch
		}

		for _, e := range c.plan.OutEdges[cur] {
			live := succeeded && (!isCond || e.SourceHandle == branch)
			tgt := c.states[e.TargetNodeID]
			if tgt.decided {
				continue
			}
			tgt.unsettled--
			if live {
				tgt.liveIn++
				if c.isBranchEdge(e) {
					tgt.liveBranch++
				}
			}
			if tgt.unsettled == 0 {
				tgt.decided = true
				if !tgt.shouldRun() {
					tgt.status = nodes.StatusSkipped
					queue = append(queue, e.TargetNodeID) // 级联跳过
				} else {
					*ready = append(*ready, e.TargetNodeID)
				}
			}
		}
	}
}

func (s *nodeState) shouldRun() bool {
	if s.branchIn > 0 {
		return s.liveBranch > 0
	}
	return s.liveIn > 0
}

func (c *coord) isBranchEdge(e dsl.DSLEdge) bool {
	if e.SourceHandle == "" {
		return false
	}
	src, ok := c.plan.Nodes[e.SourceNodeID]
	return ok && src.Data.NodeMeta.NodeType == dsl.KindCondition
}

// execNode 在 worker goroutine 执行单节点：解析输入 → 取执行器 → retry 执行 → 写池。
func (c *coord) execNode(ctx context.Context, id string, done chan<- event) {
	node := c.plan.Nodes[id]
	nodeType := node.Data.NodeMeta.NodeType
	startedAt := time.Now()

	var inputs map[string]any
	var err error
	if nodeType == dsl.KindStart {
		inputs = c.runInput
	} else {
		inputs, err = builder.ResolveInputs(node, c.pool)
	}
	if err != nil {
		done <- event{id: id, status: nodes.StatusFailed, err: err, input: inputs,
			attempt: 0, startedAt: startedAt, finishedAt: time.Now()}
		return
	}

	exec, ok := c.registry.Get(nodeType)
	if !ok {
		done <- event{id: id, status: nodes.StatusFailed,
			err: fmt.Errorf("no executor registered for node type %q", nodeType), input: inputs,
			attempt: 0, startedAt: startedAt, finishedAt: time.Now()}
		return
	}

	ec := &nodes.ExecContext{Node: node, Inputs: inputs, Pool: c.pool, RunID: c.runID}
	result, cost, attempt, execErr := runWithRetry(ctx, exec, ec, node.Data.RetryConfig)
	finishedAt := time.Now()
	if execErr != nil {
		c.pool.SetError(id, "NODE_ERROR", execErr.Error())
		done <- event{id: id, status: nodes.StatusFailed, err: execErr, input: inputs,
			cost: cost, attempt: attempt, startedAt: startedAt, finishedAt: finishedAt}
		return
	}
	c.pool.SetOutputs(id, result.Outputs)
	done <- event{id: id, status: nodes.StatusSucceeded, result: result, input: inputs,
		cost: cost, attempt: attempt, startedAt: startedAt, finishedAt: finishedAt}
}

// runWithRetry 按 RetryConfig 执行节点：每次尝试独立 timeout；父 ctx 取消则停止重试。
func runWithRetry(ctx context.Context, exec nodes.NodeExecutor, ec *nodes.ExecContext, cfg dsl.RetryConfig) (*nodes.NodeResult, time.Duration, int, error) {
	maxTries := 1
	if cfg.ShouldRetry && cfg.MaxRetries > 0 {
		maxTries = 1 + cfg.MaxRetries
	}
	timeout := time.Duration(cfg.Timeout * float64(time.Second))
	if timeout <= 0 {
		timeout = 60 * time.Second
	}

	start := time.Now()
	var lastErr error
	attempt := 0
	for attempt = 1; attempt <= maxTries; attempt++ {
		nctx, cancel := context.WithTimeout(ctx, timeout)
		res, err := exec.Execute(nctx, ec)
		cancel()
		if err == nil {
			return res, time.Since(start), attempt, nil
		}
		lastErr = err
		if ctx.Err() != nil {
			break // 父 ctx 已取消，不再重试
		}
	}
	return nil, time.Since(start), attempt, lastErr
}

// result 汇总输出（合并所有成功 End 节点）与逐节点执行记录。
func (c *coord) result() *Result {
	outputs := map[string]any{}
	for _, endID := range c.plan.EndIDs {
		st := c.states[endID]
		if st != nil && st.status == nodes.StatusSucceeded && st.result != nil {
			for k, v := range st.result.Outputs {
				outputs[k] = v
			}
		}
	}

	infos := make([]NodeExecInfo, 0, len(c.states))
	ids := make([]string, 0, len(c.states))
	for id := range c.states {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		st := c.states[id]
		info := NodeExecInfo{
			NodeID:     id,
			NodeType:   c.plan.Nodes[id].Data.NodeMeta.NodeType,
			Status:     st.status,
			Input:      st.input,
			Attempt:    st.attempt,
			Cost:       st.cost,
			StartedAt:  st.startedAt,
			FinishedAt: st.finishedAt,
		}
		if st.result != nil {
			info.Output = st.result.Outputs
		}
		if st.err != nil {
			info.Err = st.err.Error()
		}
		infos = append(infos, info)
	}
	return &Result{Outputs: outputs, Nodes: infos}
}
