package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/smart-workflow/smart-workflow/internal/httpx"
	"github.com/smart-workflow/smart-workflow/internal/service"
)

// TestFailFromErr_Mapping 守卫 service 错误 → HTTP 状态 + envelope 错误码 的映射契约。
func TestFailFromErr_Mapping(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cases := []struct {
		name       string
		err        error
		wantStatus int
		wantCode   string
	}{
		{"not found", service.ErrNotFound, http.StatusNotFound, "NOT_FOUND"},
		{"node not found", service.ErrNodeNotFound, http.StatusNotFound, "NOT_FOUND"},
		{"version lock", service.ErrVersionLock, http.StatusConflict, "VERSION_CONFLICT"},
		{"invalid json", service.ErrInvalidJSON, http.StatusBadRequest, "INVALID_JSON"},
		{"fallback", errors.New("boom"), http.StatusInternalServerError, "INTERNAL"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)

			failFromErr(c, tc.err)

			if rec.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d", rec.Code, tc.wantStatus)
			}
			var env httpx.Envelope
			if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
				t.Fatalf("unmarshal envelope: %v; body=%s", err, rec.Body.String())
			}
			if env.OK {
				t.Fatal("envelope.ok should be false")
			}
			if env.Error == nil {
				t.Fatal("envelope.error should be present")
			}
			if env.Error.Code != tc.wantCode {
				t.Fatalf("error.code = %q, want %q", env.Error.Code, tc.wantCode)
			}
		})
	}
}

// TestFailBadRequest 守卫 400 BAD_REQUEST 的形状。
func TestFailBadRequest(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)

	failBadRequest(c, "missing field x")

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	var env httpx.Envelope
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("unmarshal envelope: %v", err)
	}
	if env.OK || env.Error == nil || env.Error.Code != "BAD_REQUEST" {
		t.Fatalf("unexpected envelope: %+v", env)
	}
}

// TestPageParams 覆盖默认值、自定义值、上限截断、非法值回落。
func TestPageParams(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cases := []struct {
		name       string
		query      string
		wantLimit  int32
		wantOffset int32
	}{
		{"defaults", "", 20, 0},
		{"custom", "?limit=50&offset=10", 50, 10},
		{"limit capped", "?limit=1000", 200, 0},
		{"invalid limit ignored", "?limit=abc", 20, 0},
		{"non-positive limit ignored", "?limit=0", 20, 0},
		{"negative offset ignored", "?offset=-5", 20, 0},
		{"invalid offset ignored", "?offset=xyz", 20, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest(http.MethodGet, "/x"+tc.query, nil)

			limit, offset := pageParams(c)
			if limit != tc.wantLimit {
				t.Fatalf("limit = %d, want %d", limit, tc.wantLimit)
			}
			if offset != tc.wantOffset {
				t.Fatalf("offset = %d, want %d", offset, tc.wantOffset)
			}
		})
	}
}
