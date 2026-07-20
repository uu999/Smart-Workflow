package api

import (
	"io"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/smart-workflow/smart-workflow/internal/config"
	"github.com/smart-workflow/smart-workflow/internal/engine"
	"github.com/smart-workflow/smart-workflow/internal/httpx"
	"github.com/smart-workflow/smart-workflow/internal/service"
	"github.com/smart-workflow/smart-workflow/internal/storage/mysql"
)

// Deps 是构造路由所需的依赖（M6：接上 store + engine + services）。
type Deps struct {
	Cfg    *config.Config
	Logger *zap.Logger
	Store  *mysql.Store
	Engine *engine.Engine
}

// handlers 聚合各资源 service，供 handler 方法使用。
type handlers struct {
	cfg       *config.Config
	logger    *zap.Logger
	engine    *engine.Engine
	projects  *service.ProjectService
	apps      *service.ApplicationService
	workflows *service.WorkflowService
	runs      *service.RunService
	validate  *service.ValidateService
	nodeDebug *service.NodeDebugService
}

// NewRouter 构造 gin 引擎并注册路由。
func NewRouter(d Deps) *gin.Engine {
	if d.Cfg.Env != "dev" {
		gin.SetMode(gin.ReleaseMode)
	}

	r := gin.New()
	r.Use(gin.Recovery(), requestLogger(d.Logger))

	r.GET("/healthz", healthz)
	r.GET("/healthz/sidecar", sidecarProbe(d.Cfg))

	v1 := r.Group("/v1")
	v1.GET("/ping", func(c *gin.Context) { httpx.OK(c, gin.H{"pong": true}) })

	// M6：仅在 store 就绪时挂载资源路由（测试可只传 Cfg+Logger 复用探针）。
	if d.Store != nil {
		h := &handlers{
			cfg:       d.Cfg,
			logger:    d.Logger,
			engine:    d.Engine,
			projects:  service.NewProjectService(d.Store),
			apps:      service.NewApplicationService(d.Store),
			workflows: service.NewWorkflowService(d.Store),
			runs:      service.NewRunService(d.Store),
			validate:  service.NewValidateService(d.Store),
			nodeDebug: service.NewNodeDebugService(d.Store, d.Engine),
		}
		h.register(v1)
	}

	return r
}

// register 挂载所有 M6 资源路由。
func (h *handlers) register(v1 *gin.RouterGroup) {
	projects := v1.Group("/projects")
	{
		projects.POST("", h.createProject)
		projects.GET("", h.listProjects)
		projects.GET("/:id", h.getProject)
		projects.PUT("/:id", h.updateProject)
		projects.DELETE("/:id", h.deleteProject)
	}

	apps := v1.Group("/applications")
	{
		apps.POST("", h.createApplication)
		apps.GET("", h.listApplications)
		apps.GET("/:id", h.getApplication)
		apps.PUT("/:id", h.updateApplication)
		apps.DELETE("/:id", h.deleteApplication)
	}

	workflows := v1.Group("/workflows")
	{
		workflows.POST("", h.createWorkflow)
		workflows.GET("", h.listWorkflows)
		workflows.GET("/:id", h.getWorkflow)
		workflows.PUT("/:id", h.updateWorkflow)
		workflows.DELETE("/:id", h.deleteWorkflow)
		workflows.POST("/:id/publish", h.publishWorkflow)
		workflows.POST("/:id/validate", h.validateWorkflow)
		workflows.POST("/:id/node-debug", h.nodeDebugWorkflow)
	}

	runs := v1.Group("/runs")
	{
		runs.POST("", h.createRun)
		runs.GET("", h.listRuns)
		runs.GET("/:id", h.getRun)
	}
}

// healthz 是存活探针。
func healthz(c *gin.Context) {
	httpx.OK(c, gin.H{
		"status": "ok",
		"time":   time.Now().UTC().Format(time.RFC3339),
	})
}

// sidecarProbe 验证 Go 能否调通 Python sidecar 的 /healthz。
func sidecarProbe(cfg *config.Config) gin.HandlerFunc {
	client := &http.Client{Timeout: 3 * time.Second}
	return func(c *gin.Context) {
		resp, err := client.Get(cfg.Sidecar.BaseURL + "/healthz")
		if err != nil {
			httpx.Fail(c, http.StatusBadGateway, "SIDECAR_UNREACHABLE", err.Error(), gin.H{
				"baseurl": cfg.Sidecar.BaseURL,
			})
			return
		}
		defer func() { _ = resp.Body.Close() }()
		body, _ := io.ReadAll(resp.Body)
		httpx.OK(c, gin.H{
			"sidecar_status": resp.StatusCode,
			"sidecar_body":   string(body),
		})
	}
}

// requestLogger 记录每个请求的方法、路径、状态码、耗时。
func requestLogger(l *zap.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()
		l.Info("http",
			zap.String("method", c.Request.Method),
			zap.String("path", c.Request.URL.Path),
			zap.Int("status", c.Writer.Status()),
			zap.Duration("cost", time.Since(start)),
		)
	}
}
