package nodes

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"
)

// CodeExecutor 执行用户 Python 代码：把 code + inputs 发给 Python sidecar，
// sidecar 在子进程隔离执行并回传 outputs（对齐 PaiFlow code 节点心智）。
//
// nodeParam 字段：
//
//	code     string   用户 Python 代码，通过 sink({...}) 提交输出
//	timeout  number   秒，缺省 30
//
// SidecarURL 为空时取环境变量 SWF_SIDECAR_BASEURL，再兜底 http://127.0.0.1:8090。
type CodeExecutor struct {
	SidecarURL string
	Client     *http.Client
}

func (CodeExecutor) Type() string { return "code" }

// codeRunRequest / codeRunResponse 对齐 sidecar /run/python-code 契约。
type codeRunRequest struct {
	Code       string         `json:"code"`
	Inputs     map[string]any `json:"inputs"`
	TimeoutSec int            `json:"timeout_sec"`
}

type codeRunResponse struct {
	OK   bool `json:"ok"`
	Data struct {
		Outputs map[string]any `json:"outputs"`
		Logs    string         `json:"logs"`
	} `json:"data"`
	Error *struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

func (e CodeExecutor) Execute(ctx context.Context, ec *ExecContext) (*NodeResult, error) {
	p := ec.Node.Data.NodeParam

	code, _ := p["code"].(string)
	if code == "" {
		return nil, fmt.Errorf("code node %s missing code", ec.Node.ID)
	}

	timeoutSec := 30
	if sec, ok := toFloat(p["timeout"]); ok && sec > 0 {
		timeoutSec = int(sec)
	}

	reqBody, err := json.Marshal(codeRunRequest{
		Code:       code,
		Inputs:     ec.Inputs,
		TimeoutSec: timeoutSec,
	})
	if err != nil {
		return nil, fmt.Errorf("code node %s marshal request: %w", ec.Node.ID, err)
	}

	url := e.baseURL() + "/run/python-code"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(reqBody))
	if err != nil {
		return nil, fmt.Errorf("code node %s new request: %w", ec.Node.ID, err)
	}
	req.Header.Set("Content-Type", "application/json")

	client := e.Client
	if client == nil {
		// 客户端超时略大于代码超时，给 sidecar 收尾/回传留余量。
		client = &http.Client{Timeout: time.Duration(timeoutSec+10) * time.Second}
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("code node %s call sidecar: %w", ec.Node.ID, err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("code node %s read sidecar response: %w", ec.Node.ID, err)
	}

	var out codeRunResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("code node %s bad sidecar response: %w", ec.Node.ID, err)
	}

	if !out.OK {
		if out.Error != nil {
			return nil, fmt.Errorf("code node %s failed [%s]: %s", ec.Node.ID, out.Error.Code, out.Error.Message)
		}
		return nil, fmt.Errorf("code node %s failed with unknown sidecar error", ec.Node.ID)
	}

	return &NodeResult{Outputs: out.Data.Outputs}, nil
}

func (e CodeExecutor) baseURL() string {
	if e.SidecarURL != "" {
		return e.SidecarURL
	}
	if env := os.Getenv("SWF_SIDECAR_BASEURL"); env != "" {
		return env
	}
	return "http://127.0.0.1:8090"
}
