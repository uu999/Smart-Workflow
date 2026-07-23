package api

import (
	"context"
	"io"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/smart-workflow/smart-workflow/internal/config"
	"github.com/smart-workflow/smart-workflow/internal/engine"
	"github.com/smart-workflow/smart-workflow/internal/eventbus"
	"github.com/smart-workflow/smart-workflow/internal/httpx"
	"github.com/smart-workflow/smart-workflow/internal/service"
	"github.com/smart-workflow/smart-workflow/internal/storage/mysql"
)

// RunSubmitter 抽象"提交一个已落 pending 的 run 去后台执行"。
// 两种实现满足它：进程内 RunDispatcher（M6 兜底）与 asynq 入队器（M9-a），
// createRun handler 只依赖此接口，切换执行后端不改 handler。
type RunSubmitter interface {
	// Submit 尝试提交 run 去执行；返回 false 表示拒绝（满载/关停/入队失败），
	// 调用方应把 run 落 failed 并回 503。
	Submit(runID string) bool
	// Shutdown 优雅停机（drain 在途 / 关闭客户端）。
	Shutdown(ctx context.Context) error
}

// Deps 是构造路由所需的依赖（M6：接上 store + engine + services）。
type Deps struct {
	Cfg    *config.Config
	Logger *zap.Logger
	Store  *mysql.Store
	Engine *engine.Engine
	// Runner 后台运行提交器（RunDispatcher 或 asynq 入队器）。为空时按默认
	// 进程内 dispatcher 构造；调用方（main）通常自建并持有引用以便优雅退出时 Shutdown。
	Runner RunSubmitter
	// EventSource 运行事件订阅源（M9-b SSE）。dispatcher 模式传共享 MemHub，
	// asynq 模式传 RedisSource。为空时 GET /runs/:id/events 回 501（CLI 回退轮询）。
	EventSource eventbus.Source
}

// handlers 聚合各资源 service，供 handler 方法使用。
type handlers struct {
	cfg       *config.Config
	logger    *zap.Logger
	engine    *engine.Engine
	runner    RunSubmitter
	events    eventbus.Source
	projects  *service.ProjectService
	apps      *service.ApplicationService
	datasets  *service.DatasetService
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
	// M10 加固：可选鉴权 + 限流（挂在 v1，healthz 不受影响）。
	// 两者未配置时为 no-op（空 APIKeys=放行、rps<=0=不限流），既有测试零改动。
	v1.Use(authMiddlewares(d.Cfg.Auth)...)
	v1.GET("/ping", func(c *gin.Context) { httpx.OK(c, gin.H{"pong": true}) })

	// M6：仅在 store 就绪时挂载资源路由（测试可只传 Cfg+Logger 复用探针）。
	if d.Store != nil {
		runner := d.Runner
		if runner == nil {
			runner = NewRunDispatcher(d.Engine, d.Logger, 0)
		}
		h := &handlers{
			cfg:       d.Cfg,
			logger:    d.Logger,
			engine:    d.Engine,
			runner:    runner,
			events:    d.EventSource,
			projects:  service.NewProjectService(d.Store),
			apps:      service.NewApplicationService(d.Store),
			datasets:  service.NewDatasetService(d.Store),
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

	datasets := v1.Group("/datasets")
	{
		datasets.POST("", h.createDataset)
		datasets.GET("", h.listDatasets)
		datasets.GET("/:id", h.getDataset)
		datasets.PUT("/:id", h.updateDataset)
		datasets.DELETE("/:id", h.deleteDataset)
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
		runs.GET("/:id/events", h.streamRun) // M9-b：SSE 实时事件流
	}

	// 无状态单节点调试（风险1）：吃渲染后的 DSL 节点，不需 workflowID，
	// 支撑 CLI "本地 validate/preview → node-debug → upload" 闭环。
	v1.POST("/node-debug", h.debugNode)
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
