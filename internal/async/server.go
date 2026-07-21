package async

import (
	"github.com/hibiken/asynq"

	"github.com/smart-workflow/smart-workflow/internal/config"
)

// ServerConfig 配置 worker 的 asynq server。
type ServerConfig struct {
	Concurrency int // worker 并发度，<=0 时用 asynq 默认
}

// NewServer 构造消费三优先级队列（debug/default/batch）的 asynq server。
func NewServer(redis config.RedisConfig, cfg ServerConfig) *asynq.Server {
	c := asynq.Config{
		Queues:          QueueWeights(),
		StrictPriority:  false, // 按权重比例分配，非严格清空高优先才碰低优先
	}
	if cfg.Concurrency > 0 {
		c.Concurrency = cfg.Concurrency
	}
	return asynq.NewServer(redisOpt(redis), c)
}

// NewMux 注册运行任务处理器，返回可交给 Server.Run 的 handler。
func NewMux(exec RunExecutor) *asynq.ServeMux {
	mux := asynq.NewServeMux()
	mux.Handle(TypeRunWorkflow, NewRunProcessor(exec))
	return mux
}
