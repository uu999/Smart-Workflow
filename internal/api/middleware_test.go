package api

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/smart-workflow/smart-workflow/internal/config"
)

// newTestRouter 用给定 Auth 配置挂中间件，注册几个探测路由。
func newTestRouter(auth config.AuthConfig) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/healthz", func(c *gin.Context) { c.String(http.StatusOK, "ok") })
	v1 := r.Group("/v1")
	v1.Use(authMiddlewares(auth)...)
	v1.GET("/ping", func(c *gin.Context) { c.String(http.StatusOK, "pong") })
	v1.GET("/runs/:id/events", func(c *gin.Context) { c.String(http.StatusOK, "sse") })
	v1.GET("/workflows", func(c *gin.Context) { c.String(http.StatusOK, "list") })
	return r
}

func do(r *gin.Engine, method, path, key string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, nil)
	if key != "" {
		req.Header.Set("X-API-Key", key)
	}
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

// TestAuth_EmptyKeysPassThrough 验证「空配置=放行」——不设 key 也能访问（兼容既有测试）。
func TestAuth_EmptyKeysPassThrough(t *testing.T) {
	r := newTestRouter(config.AuthConfig{}) // 无 keys、无限流
	if rec := do(r, "GET", "/v1/workflows", ""); rec.Code != http.StatusOK {
		t.Fatalf("empty auth should pass, got %d", rec.Code)
	}
}

// TestAuth_ValidAndInvalidKey 验证配置 keys 后：正确 key 放行，错误/缺失 401。
func TestAuth_ValidAndInvalidKey(t *testing.T) {
	r := newTestRouter(config.AuthConfig{APIKeys: []string{"secret1", "secret2"}})

	if rec := do(r, "GET", "/v1/workflows", "secret1"); rec.Code != http.StatusOK {
		t.Fatalf("valid key1 should pass, got %d", rec.Code)
	}
	if rec := do(r, "GET", "/v1/workflows", "secret2"); rec.Code != http.StatusOK {
		t.Fatalf("valid key2 should pass, got %d", rec.Code)
	}
	if rec := do(r, "GET", "/v1/workflows", "wrong"); rec.Code != http.StatusUnauthorized {
		t.Fatalf("wrong key should 401, got %d", rec.Code)
	}
	if rec := do(r, "GET", "/v1/workflows", ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("missing key should 401, got %d", rec.Code)
	}
}

// TestAuth_ExemptPaths 验证 ping 与 SSE 事件流即使开鉴权也豁免。
func TestAuth_ExemptPaths(t *testing.T) {
	r := newTestRouter(config.AuthConfig{APIKeys: []string{"secret"}})
	if rec := do(r, "GET", "/v1/ping", ""); rec.Code != http.StatusOK {
		t.Fatalf("ping should be exempt from auth, got %d", rec.Code)
	}
	if rec := do(r, "GET", "/v1/runs/run1/events", ""); rec.Code != http.StatusOK {
		t.Fatalf("SSE events should be exempt from auth, got %d", rec.Code)
	}
}

// TestRateLimit_AllowsWithinBurstThen429 验证令牌桶：突发内放行，超出 429。
func TestRateLimit_AllowsWithinBurstThen429(t *testing.T) {
	// rps 很低、burst=2：前 2 个请求过，第 3 个应被限。
	r := newTestRouter(config.AuthConfig{RateRPS: 0.001, RateBurst: 2})
	if rec := do(r, "GET", "/v1/workflows", ""); rec.Code != http.StatusOK {
		t.Fatalf("req1 should pass, got %d", rec.Code)
	}
	if rec := do(r, "GET", "/v1/workflows", ""); rec.Code != http.StatusOK {
		t.Fatalf("req2 should pass, got %d", rec.Code)
	}
	if rec := do(r, "GET", "/v1/workflows", ""); rec.Code != http.StatusTooManyRequests {
		t.Fatalf("req3 should be 429, got %d", rec.Code)
	}
}

// TestRateLimit_ExemptSSENotLimited 验证 SSE 长连接不被限流误伤（豁免不消耗令牌）。
func TestRateLimit_ExemptSSENotLimited(t *testing.T) {
	r := newTestRouter(config.AuthConfig{RateRPS: 0.001, RateBurst: 1})
	// 连打多次 SSE，均应放行（不受桶限制）。
	for i := 0; i < 5; i++ {
		if rec := do(r, "GET", "/v1/runs/run1/events", ""); rec.Code != http.StatusOK {
			t.Fatalf("SSE req %d should be exempt from rate limit, got %d", i, rec.Code)
		}
	}
}

// TestRateLimit_Disabled 验证 rps<=0 时不限流。
func TestRateLimit_Disabled(t *testing.T) {
	r := newTestRouter(config.AuthConfig{RateRPS: 0})
	for i := 0; i < 20; i++ {
		if rec := do(r, "GET", "/v1/workflows", ""); rec.Code != http.StatusOK {
			t.Fatalf("no rate limit configured, req %d should pass, got %d", i, rec.Code)
		}
	}
}
