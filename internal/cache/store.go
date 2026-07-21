// Package cache 提供 M9-a 的 Redis 缓存层：定义缓存（swf:def）、引擎缓存（swf:plan）、
// 运行态镜像（swf:run）与运行锁的底层存储抽象。
//
// 分层：Store 是最小键值接口，engine / async 只依赖它，便于用内存实现做单测、
// 生产用 Redis 实现。键命名集中在 keys.go，避免散落拼串。
package cache

import (
	"context"
	"time"
)

// Store 是最小键值存储抽象。值统一按 []byte（调用方自行 JSON 编解码）。
// ErrMiss 由 GetBytes 在键不存在时返回，供调用方区分"未命中"与"真错误"。
type Store interface {
	GetBytes(ctx context.Context, key string) ([]byte, error)
	SetBytes(ctx context.Context, key string, val []byte, ttl time.Duration) error
	Del(ctx context.Context, key string) error
}
