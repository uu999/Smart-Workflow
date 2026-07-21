// Package eventbus 连接运行事件的发布端与订阅端（M9-b）。
//
// 两条路径（对应部署两种形态）：
//   - MemHub：进程内 pub/sub，同时实现 runevent.Emitter 与 Source。
//     用于「无 Redis」的进程内 dispatcher 模式——engine 在 server 进程发事件，
//     SSE 在同进程订阅。
//   - Redis Stream：RedisEmitter(XADD) + RedisSource(XREAD)，跨进程。
//     用于 asynq 模式——worker 进程发事件，server 进程的 SSE 订阅。
//
// Source 是 SSE handler 依赖的订阅抽象，两种实现都满足它。
package eventbus

import (
	"context"

	"github.com/smart-workflow/smart-workflow/internal/runevent"
)

// Source 是事件订阅端：给定 runID，返回一个事件 channel，
// 收到 run_end 或 ctx 取消后关闭 channel。
type Source interface {
	Subscribe(ctx context.Context, runID string) (<-chan runevent.RunEvent, error)
}
