package cache

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestMemStore_RoundTrip(t *testing.T) {
	ctx := context.Background()
	s := NewMemStore()

	// miss
	if _, err := s.GetBytes(ctx, "k"); !errors.Is(err, ErrMiss) {
		t.Fatalf("empty get: want ErrMiss, got %v", err)
	}

	// set + get
	if err := s.SetBytes(ctx, "k", []byte("v"), 0); err != nil {
		t.Fatalf("set: %v", err)
	}
	got, err := s.GetBytes(ctx, "k")
	if err != nil || string(got) != "v" {
		t.Fatalf("get: %q err=%v", got, err)
	}

	// del
	if err := s.Del(ctx, "k"); err != nil {
		t.Fatalf("del: %v", err)
	}
	if _, err := s.GetBytes(ctx, "k"); !errors.Is(err, ErrMiss) {
		t.Fatalf("after del: want ErrMiss, got %v", err)
	}
}

func TestMemStore_TTLExpiry(t *testing.T) {
	ctx := context.Background()
	s := NewMemStore()
	if err := s.SetBytes(ctx, "k", []byte("v"), 20*time.Millisecond); err != nil {
		t.Fatalf("set: %v", err)
	}
	if _, err := s.GetBytes(ctx, "k"); err != nil {
		t.Fatalf("immediate get should hit: %v", err)
	}
	time.Sleep(35 * time.Millisecond)
	if _, err := s.GetBytes(ctx, "k"); !errors.Is(err, ErrMiss) {
		t.Fatalf("after ttl: want ErrMiss, got %v", err)
	}
}

func TestMemStore_ReturnsCopy(t *testing.T) {
	ctx := context.Background()
	s := NewMemStore()
	_ = s.SetBytes(ctx, "k", []byte("abc"), 0)
	got, _ := s.GetBytes(ctx, "k")
	got[0] = 'X' // 改动返回值不应污染内部存储
	again, _ := s.GetBytes(ctx, "k")
	if string(again) != "abc" {
		t.Fatalf("internal store mutated: %q", again)
	}
}

func TestKeys(t *testing.T) {
	cases := []struct {
		got, want string
	}{
		{DefKey("wf_1", 3), "swf:def:wf_1:3"},
		{DefKey("wf_1", -1), "swf:def:wf_1:-1"},
		{PlanKey("wf_1", 0), "swf:plan:wf_1:0"},
		{RunKey("run_9"), "swf:run:run_9"},
	}
	for _, c := range cases {
		if c.got != c.want {
			t.Errorf("key = %q, want %q", c.got, c.want)
		}
	}
}

// 编译期确认 MemStore 满足 Store。
var _ Store = (*MemStore)(nil)
