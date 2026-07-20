package nodes

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// HTTPExecutor 调用外部 HTTP API。nodeParam 字段：
//
//	method   string           GET/POST/...（缺省 GET）
//	url      string           支持 {{port}} 占位符
//	headers  map[string]any   请求头，值支持 {{port}} 占位符
//	body     string|object    请求体；string 支持 {{port}} 占位符，object 序列化为 JSON
//	timeout  number           秒，缺省 30
//
// 输出：status_code(int) / body(string) / json(any，body 可解析为 JSON 时)。
type HTTPExecutor struct {
	Client *http.Client
}

func (HTTPExecutor) Type() string { return "http" }

func (e HTTPExecutor) Execute(ctx context.Context, ec *ExecContext) (*NodeResult, error) {
	p := ec.Node.Data.NodeParam

	rawURL, _ := p["url"].(string)
	if rawURL == "" {
		return nil, fmt.Errorf("http node %s missing url", ec.Node.ID)
	}
	url := renderTemplate(rawURL, ec.Inputs)

	method := strings.ToUpper(strings.TrimSpace(fmt.Sprintf("%v", p["method"])))
	if method == "" || method == "<NIL>" {
		method = http.MethodGet
	}

	body, err := buildBody(p["body"], ec.Inputs)
	if err != nil {
		return nil, fmt.Errorf("http node %s build body: %w", ec.Node.ID, err)
	}

	req, err := http.NewRequestWithContext(ctx, method, url, body)
	if err != nil {
		return nil, fmt.Errorf("http node %s new request: %w", ec.Node.ID, err)
	}
	applyHeaders(req, p["headers"], ec.Inputs)

	client := e.Client
	if client == nil {
		client = &http.Client{Timeout: timeoutOf(p, 30*time.Second)}
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("http node %s request failed: %w", ec.Node.ID, err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("http node %s read body: %w", ec.Node.ID, err)
	}

	out := map[string]any{
		"status_code": resp.StatusCode,
		"body":        string(raw),
	}
	var parsed any
	if json.Unmarshal(raw, &parsed) == nil {
		out["json"] = parsed
	}
	return &NodeResult{Outputs: out}, nil
}

// buildBody 把 nodeParam.body 转成请求体 Reader。
// string 走模板替换；object/其它序列化为 JSON；nil 返回 nil。
func buildBody(raw any, inputs map[string]any) (io.Reader, error) {
	switch b := raw.(type) {
	case nil:
		return nil, nil
	case string:
		if b == "" {
			return nil, nil
		}
		return strings.NewReader(renderTemplate(b, inputs)), nil
	default:
		data, err := json.Marshal(b)
		if err != nil {
			return nil, err
		}
		return strings.NewReader(string(data)), nil
	}
}

// applyHeaders 写入请求头，值支持 {{port}} 占位符。
func applyHeaders(req *http.Request, raw any, inputs map[string]any) {
	hm, ok := raw.(map[string]any)
	if !ok {
		return
	}
	for k, v := range hm {
		req.Header.Set(k, renderTemplate(fmt.Sprintf("%v", v), inputs))
	}
}

// timeoutOf 读取 nodeParam.timeout（秒），非法则用默认值。
func timeoutOf(p map[string]any, def time.Duration) time.Duration {
	if sec, ok := toFloat(p["timeout"]); ok && sec > 0 {
		return time.Duration(sec * float64(time.Second))
	}
	return def
}
