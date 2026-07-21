package cache

import (
	"context"
	"sync"
	"time"
)

// MemStore 是 Store 的内存实现，供单测使用（无需真 Redis）。
// 支持 TTL 过期（惰性检查），并发安全。
type MemStore struct {
	mu   sync.Mutex
	data map[string]memEntry
}

type memEntry struct {
	val       []byte
	expiresAt time.Time // 零值 = 永不过期
}

// NewMemStore 构造一个空的内存 Store。
func NewMemStore() *MemStore {
	return &MemStore{data: make(map[string]memEntry)}
}

func (m *MemStore) GetBytes(_ context.Context, key string) ([]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	e, ok := m.data[key]
	if !ok {
		return nil, ErrMiss
	}
	if !e.expiresAt.IsZero() && time.Now().After(e.expiresAt) {
		delete(m.data, key)
		return nil, ErrMiss
	}
	// 返回副本，避免调用方改动内部存储。
	out := make([]byte, len(e.val))
	copy(out, e.val)
	return out, nil
}

func (m *MemStore) SetBytes(_ context.Context, key string, val []byte, ttl time.Duration) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	cp := make([]byte, len(val))
	copy(cp, val)
	e := memEntry{val: cp}
	if ttl > 0 {
		e.expiresAt = time.Now().Add(ttl)
	}
	m.data[key] = e
	return nil
}

func (m *MemStore) Del(_ context.Context, key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.data, key)
	return nil
}
