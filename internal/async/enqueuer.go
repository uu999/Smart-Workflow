package async

import (
	"errors"

	"github.com/hibiken/asynq"

	"github.com/smart-workflow/smart-workflow/internal/config"
)

// DefaultMaxRetry 是运行任务的默认重试次数。
const DefaultMaxRetry = 3

// Enqueuer 封装 asynq.Client，负责把 run 入队。
type Enqueuer struct {
	client   *asynq.Client
	maxRetry int
}

// redisOpt 把 config.Redis 转成 asynq 的连接选项。
func redisOpt(cfg config.RedisConfig) asynq.RedisClientOpt {
	return asynq.RedisClientOpt{Addr: cfg.Addr, Password: cfg.Password, DB: cfg.DB}
}

// NewEnqueuer 用 config.Redis 构造入队器。
func NewEnqueuer(cfg config.RedisConfig) *Enqueuer {
	return &Enqueuer{client: asynq.NewClient(redisOpt(cfg)), maxRetry: DefaultMaxRetry}
}

// Enqueue 把一个已落 pending 的 run 入队到指定优先级队列。
// 返回 (accepted, err)：
//   - 已存在同 runID 任务（去重锁命中）→ accepted=true, err=nil（幂等，视为已接受）
//   - 其他 Redis 错误 → accepted=false, err（调用方据此落 failed + 回 503）
func (e *Enqueuer) Enqueue(runID, queue string) (bool, error) {
	task, err := NewRunTask(runID, queue, e.maxRetry)
	if err != nil {
		return false, err
	}
	_, err = e.client.Enqueue(task)
	if err != nil {
		// 去重锁：同一 run 已在队列/执行中，幂等接受，不重复跑。
		if errors.Is(err, asynq.ErrDuplicateTask) || errors.Is(err, asynq.ErrTaskIDConflict) {
			return true, nil
		}
		return false, err
	}
	return true, nil
}

// Close 关闭底层 client。
func (e *Enqueuer) Close() error {
	return e.client.Close()
}

// QueueWeights 返回 worker Server 的优先级权重（debug 最高、batch 最低）。
// asynq 按权重比例分配 worker 拉取各队列的概率。
func QueueWeights() map[string]int {
	return map[string]int{
		QueueDebug:   6,
		QueueDefault: 3,
		QueueBatch:   1,
	}
}
