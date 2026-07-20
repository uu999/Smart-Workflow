package cli

import (
	"time"
)

// pollRun 轮询 GET /v1/runs/{id} 直到 succeeded/failed，或超时（默认 30s）。
// 返回最终的 run 视图（map 形态，直接进 envelope）。
func (a *appCtx) pollRun(runID string) (any, error) {
	deadline := time.Now().Add(30 * time.Second)
	for {
		var view map[string]any
		if err := a.client().doJSON("GET", "/v1/runs/"+runID, nil, &view); err != nil {
			return nil, err
		}
		status, _ := view["status"].(string)
		if status == "succeeded" || status == "failed" || status == "canceled" {
			return view, nil
		}
		if time.Now().After(deadline) {
			return view, newErr("RUN_TIMEOUT", "run did not reach terminal state within 30s",
				"swf run ... 后用 GET /v1/runs/{id} 继续轮询")
		}
		time.Sleep(300 * time.Millisecond)
	}
}
