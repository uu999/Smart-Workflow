package api

import (
	"crypto/subtle"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"golang.org/x/time/rate"

	"github.com/smart-workflow/smart-workflow/internal/config"
	"github.com/smart-workflow/smart-workflow/internal/httpx"
)

// apiKeyHeader 是携带 API Key 的请求头名。
const apiKeyHeader = "X-API-Key"

// isExemptPath 判断路径是否豁免鉴权/限流：
//   - /v1/ping：连通性探测
//   - SSE 事件流 /v1/runs/{id}/events：长连接，限流会误伤；鉴权在建流的 GET /runs 已可控
// 注：/healthz* 挂在 root，不经过 v1 中间件，天然豁免。
func isExemptPath(path string) bool {
	if path == "/v1/ping" {
		return true
	}
	return strings.HasPrefix(path, "/v1/runs/") && strings.HasSuffix(path, "/events")
}

// apiKeyAuth 返回 API Key 鉴权中间件。keys 为空时返回 no-op（放行，兼容 dev 与既有测试）。
// 命中豁免路径直接放行。用常量时间比较防时序侧信道。
func apiKeyAuth(keys []string) gin.HandlerFunc {
	if len(keys) == 0 {
		return func(c *gin.Context) { c.Next() } // 鉴权关闭
	}
	// 预处理为 [][]byte，避免每请求重复转换。
	want := make([][]byte, len(keys))
	for i, k := range keys {
		want[i] = []byte(k)
	}
	return func(c *gin.Context) {
		if isExemptPath(c.Request.URL.Path) {
			c.Next()
			return
		}
		got := []byte(c.GetHeader(apiKeyHeader))
		for _, w := range want {
			if subtle.ConstantTimeCompare(got, w) == 1 {
				c.Next()
				return
			}
		}
		httpx.Fail(c, http.StatusUnauthorized, "UNAUTHORIZED",
			"missing or invalid "+apiKeyHeader, gin.H{"hint": "set --api-key / SWF_API_KEY"})
		c.Abort()
	}
}

// rateLimit 返回全局令牌桶限流中间件。rps<=0 时返回 no-op（不限流）。
// 命中豁免路径不消耗令牌（SSE 长连接不被限流误伤）。
// 说明：进程内全局桶——自包含部署够用；多实例/按租户配额应走外置网关（README）。
func rateLimit(rps float64, burst int) gin.HandlerFunc {
	if rps <= 0 {
		return func(c *gin.Context) { c.Next() } // 限流关闭
	}
	if burst <= 0 {
		burst = int(rps)
		if burst < 1 {
			burst = 1
		}
	}
	limiter := rate.NewLimiter(rate.Limit(rps), burst)
	return func(c *gin.Context) {
		if isExemptPath(c.Request.URL.Path) {
			c.Next()
			return
		}
		if !limiter.Allow() {
			httpx.Fail(c, http.StatusTooManyRequests, "RATE_LIMITED",
				"too many requests, slow down", gin.H{"limit_rps": rps})
			c.Abort()
			return
		}
		c.Next()
	}
}

// authMiddlewares 按配置组装鉴权+限流中间件（供 v1 group 使用）。
// 两者均在关闭时为 no-op，故未配置时对既有行为零影响。
func authMiddlewares(cfg config.AuthConfig) []gin.HandlerFunc {
	return []gin.HandlerFunc{
		rateLimit(cfg.RateRPS, cfg.RateBurst),
		apiKeyAuth(cfg.APIKeys),
	}
}
