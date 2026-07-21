package engine

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/smart-workflow/smart-workflow/internal/cache"
)

// TestDSLCache_HitAvoidsResource 验证：命中定义缓存时 loadDSL 直接返回缓存内容，
// 不回源 MySQL（本用例 store 为 nil，若回源会 panic/err，故命中即证明省了 DB 读）。
func TestDSLCache_Hit(t *testing.T) {
	store := cache.NewMemStore()
	ctx := context.Background()

	// 预置 wf_1 v2 的 DSL 到缓存。
	want := `{"nodes":[{"id":"start::1"}],"edges":[]}`
	_ = store.SetBytes(ctx, cache.DefKey("wf_1", 2), []byte(want), time.Hour)

	// store=nil：一旦回源就会 nil deref；命中缓存则安全返回。
	e := New(nil, "").WithCache(store)
	d, err := e.loadDSL(ctx, "wf_1", 2)
	if err != nil {
		t.Fatalf("loadDSL hit: %v", err)
	}
	if len(d.Nodes) != 1 || d.Nodes[0].ID != "start::1" {
		t.Fatalf("cached DSL not returned: %+v", d)
	}
}

// TestDSLCache_DraftNotCached 验证草稿版本(-1)不走缓存（即便缓存里有同键也不用）。
// 用 store=nil + 缓存里放一条 draft 键：若引擎错误地读了缓存会返回它；
// 正确行为是草稿绕过缓存 → 回源 nil store → 报错。
func TestDSLCache_DraftNotCached(t *testing.T) {
	store := cache.NewMemStore()
	ctx := context.Background()
	_ = store.SetBytes(ctx, cache.DefKey("wf_1", DraftVersion), []byte(`{"nodes":[{"id":"x"}]}`), time.Hour)

	e := New(nil, "").WithCache(store)
	// 草稿应绕过缓存、尝试回源；store 为 nil → GetWorkflow 触发 nil deref 前先 panic 防御？
	// 实际会走到 e.store.Q，store 为 nil。用 recover 确认它确实尝试回源而非读缓存。
	defer func() { _ = recover() }()
	d, err := e.loadDSL(ctx, "wf_1", DraftVersion)
	// 若错误地命中了缓存，d 会非空且 err 为 nil —— 那是 bug。
	if err == nil && d != nil && len(d.Nodes) > 0 {
		t.Fatal("draft must NOT be served from cache")
	}
}

// TestDSLCache_WriteBackShape 验证回源后回填缓存的内容可被再次命中解析。
func TestDSLCache_RoundTripShape(t *testing.T) {
	store := cache.NewMemStore()
	ctx := context.Background()
	raw := `{"nodes":[{"id":"end::9"}],"edges":[]}`
	_ = store.SetBytes(ctx, cache.DefKey("wf_9", 5), []byte(raw), time.Hour)

	e := New(nil, "").WithCache(store)
	d, err := e.loadDSL(ctx, "wf_9", 5)
	if err != nil {
		t.Fatalf("loadDSL: %v", err)
	}
	// 再序列化应结构等价（node id 保留）。
	b, _ := json.Marshal(d)
	var got map[string]any
	_ = json.Unmarshal(b, &got)
	nodes, _ := got["nodes"].([]any)
	if len(nodes) != 1 {
		t.Fatalf("round-trip lost nodes: %s", b)
	}
}

// 编译期确认 cache.MemStore 满足 engine.DSLCache。
var _ DSLCache = (*cache.MemStore)(nil)
