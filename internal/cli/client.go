package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// Client 是 CLI 调服务端 API 的薄封装。真跑类命令（node-debug/run/upload）
// 走它，把 sidecar/DB 访问留在服务端，CLI 自身保持无状态、可离线做 IR 编辑。
type Client struct {
	BaseURL string
	apiKey  string // 可选：非空时每个请求带 X-API-Key
	http    *http.Client
}

// NewClient 构造指向 baseURL 的客户端。
func NewClient(baseURL string) *Client {
	return &Client{
		BaseURL: baseURL,
		http:    &http.Client{Timeout: 60 * time.Second},
	}
}

// WithAPIKey 注入 API Key（链式）。空串时不带认证头（服务端未开鉴权时的常态）。
func (c *Client) WithAPIKey(key string) *Client {
	c.apiKey = key
	return c
}

// setAuth 若配置了 API Key，则给请求加 X-API-Key 头。
func (c *Client) setAuth(req *http.Request) {
	if c.apiKey != "" {
		req.Header.Set("X-API-Key", c.apiKey)
	}
}

// serverEnvelope 是服务端响应的解析体（对齐 httpx.Envelope）。
type serverEnvelope struct {
	OK    bool            `json:"ok"`
	Data  json.RawMessage `json:"data"`
	Error *struct {
		Code    string          `json:"code"`
		Message string          `json:"message"`
		Details json.RawMessage `json:"details"`
	} `json:"error"`
}

// doJSON 发一个 JSON 请求并把成功响应的 data 解到 out。
// 服务端返回 ok=false 时，转成 *CLIErr（保留服务端 code/message/details），
// 让 Agent 无论错误来自 CLI 本地还是服务端，都拿到同构结构化反馈。
func (c *Client) doJSON(method, path string, body any, out any) error {
	var reader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return newErr("BAD_REQUEST", fmt.Sprintf("marshal request: %v", err), "")
		}
		reader = bytes.NewReader(b)
	}

	req, err := http.NewRequest(method, c.BaseURL+path, reader)
	if err != nil {
		return newErr("BAD_REQUEST", err.Error(), "")
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	c.setAuth(req)

	resp, err := c.http.Do(req)
	if err != nil {
		return newErr("SERVER_UNREACHABLE", err.Error(),
			"is swf-server running? check --server / SWF_SERVER_URL")
	}
	defer func() { _ = resp.Body.Close() }()

	raw, _ := io.ReadAll(resp.Body)
	var env serverEnvelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return newErr("BAD_RESPONSE",
			fmt.Sprintf("status %d, unparseable body: %s", resp.StatusCode, truncate(string(raw), 200)), "")
	}

	if !env.OK {
		if env.Error == nil {
			return newErr("SERVER_ERROR", fmt.Sprintf("status %d", resp.StatusCode), "")
		}
		ce := &CLIErr{Code: env.Error.Code, Message: env.Error.Message}
		if len(env.Error.Details) > 0 {
			var d any
			if json.Unmarshal(env.Error.Details, &d) == nil {
				ce.Details = d
			}
		}
		return ce
	}

	if out != nil && len(env.Data) > 0 {
		if err := json.Unmarshal(env.Data, out); err != nil {
			return newErr("BAD_RESPONSE", fmt.Sprintf("decode data: %v", err), "")
		}
	}
	return nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
