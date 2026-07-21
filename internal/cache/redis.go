package cache

import (
	"context"
	"errors"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/smart-workflow/smart-workflow/internal/config"
)

// RedisStore 是基于 go-redis v9 的 Store 实现。
type RedisStore struct {
	cli *redis.Client
}

// NewRedisClient 用 config.Redis 构造一个 go-redis 客户端。抽出来是为了让
// cache（定义缓存）与 eventbus（Stream 事件）共享同一份连接配置与连接池，
// 而不是各自拼一遍 Options（DI 友好，避免配置漂移）。
func NewRedisClient(cfg config.RedisConfig) *redis.Client {
	return redis.NewClient(&redis.Options{
		Addr:     cfg.Addr,
		Password: cfg.Password,
		DB:       cfg.DB,
	})
}

// NewRedisStore 用 config.Redis 构造一个 Redis Store。调用方负责在退出时 Close。
func NewRedisStore(cfg config.RedisConfig) *RedisStore {
	return &RedisStore{cli: NewRedisClient(cfg)}
}

// NewRedisStoreFromClient 复用已有的 *redis.Client（例如与 asynq 共享连接配置）。
func NewRedisStoreFromClient(cli *redis.Client) *RedisStore {
	return &RedisStore{cli: cli}
}

func (s *RedisStore) GetBytes(ctx context.Context, key string) ([]byte, error) {
	b, err := s.cli.Get(ctx, key).Bytes()
	if errors.Is(err, redis.Nil) {
		return nil, ErrMiss
	}
	if err != nil {
		return nil, err
	}
	return b, nil
}

func (s *RedisStore) SetBytes(ctx context.Context, key string, val []byte, ttl time.Duration) error {
	return s.cli.Set(ctx, key, val, ttl).Err()
}

func (s *RedisStore) Del(ctx context.Context, key string) error {
	return s.cli.Del(ctx, key).Err()
}

// Ping 校验 Redis 连通性（server/worker 启动时可调）。
func (s *RedisStore) Ping(ctx context.Context) error {
	return s.cli.Ping(ctx).Err()
}

// Close 关闭底层连接。
func (s *RedisStore) Close() error {
	return s.cli.Close()
}
