// Package api 暴露 HTTP 端点。
//
// 端点列表（Phase 0）：
//   - GET /healthz        200 {"status":"ok"}
//   - GET /api/healthz    200 {"status":"ok","uptime":"3s","db":"ok","version":"dev"}
//   - GET /api/version    200 {"name":"cncachehub","version":"dev","go":"go1.22.x","commit":"local"}
//
// 设计原则：
//   - 错误响应统一 {"error":{"code":"...","message":"..."}}；
//   - 通过 Options 注入依赖（DB / version / startTime），避免包级状态；
//   - middleware 顺序：RequestID → RealIP → Logger（用 internal/log）→ Recoverer；
//   - 端口 / 地址由调用方通过 Server 注入，本包不直接 ListenAndServe。
package api

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync/atomic"
	"time"

	"github.com/cncachehub/server/internal/access"
	dnsserver "github.com/cncachehub/server/internal/dns"
	"github.com/cncachehub/server/internal/diagnostics"
	"github.com/cncachehub/server/internal/metrics"
	"github.com/cncachehub/server/internal/ratelimit"
	"github.com/cncachehub/server/internal/storage"

	"github.com/go-chi/chi/v5"
	chimw "github.com/go-chi/chi/v5/middleware"

	logpkg "github.com/cncachehub/server/internal/log"
)

// BuildInfo 描述构建时信息。
type BuildInfo struct {
	Name    string // 应用名（固定 "cncachehub"）
	Version string // 版本（默认 "dev"）
	Go      string // 编译时 Go 版本（runtime.Version()）
	Commit  string // 提交 SHA（默认 "local"）
}

// Cipher 是凭据加密接口（§9.7.3）。main.go 注入 internal/crypto 的实现。
//
// 接口而不是直接 import crypto 包：避免 api 跟 main 之间的循环依赖（main 同时 import api 和 crypto）。
type Cipher interface {
	Encrypt(plaintext []byte) ([]byte, error)
	Decrypt(ciphertext []byte) ([]byte, error)
}

// AccessLogRecord 是 /v2/* 访问日志的简版（与 storage.AccessLogRecord 同形但解耦）。
//
// api 包不直接 import storage（避免循环），所以这里独立定义 record 类型。
// main.go 注入 AccessLogWriter 时负责转换。
type AccessLogRecord struct {
	ID           int64  `json:"id"`
	CreatedAt    int64  `json:"createdAt"` // unix 秒
	Method       string `json:"method"`
	Path         string `json:"path"`
	Status       int    `json:"status"`
	DurationMs   int64  `json:"durationMs"`
	Cached       bool   `json:"cached"`
	Bypassed     bool   `json:"bypassed"`
	BypassReason string `json:"bypassReason"` // PRD §9.6.4: size_limit / disk_low / ''
	ClientIP     string `json:"clientIp"`
	Bytes        int64  `json:"bytes"`
	Error        string `json:"error"`
}

// LogFilter 封装 GET /api/logs 查询参数（与 storage.LogFilter 同形但解耦）。
type LogFilter struct {
	Status    int    `json:"status,omitempty"`    // 精确状态码，0=不限
	StatusCls int    `json:"statusCls,omitempty"` // 1-5 = 匹配 1xx-5xx，0=不限
	Method    string `json:"method,omitempty"`
	Path      string `json:"path,omitempty"`      // 子串搜索
	Cached    *bool  `json:"cached,omitempty"`    // nil=不限
	Bypassed  *bool  `json:"bypassed,omitempty"`  // nil=不限
	ClientIP  string `json:"clientIp,omitempty"`
	StartAt   int64  `json:"startAt,omitempty"`   // unix 秒
	EndAt     int64  `json:"endAt,omitempty"`     // unix 秒
}

// AccessLogWriter 把访问日志异步落盘（由 main.go 注入，连接 storage.DB）。
type AccessLogWriter interface {
	WriteAccessLog(ctx context.Context, rec AccessLogRecord) error
}

// Options 注入 Server 依赖。
type Options struct {
	DB        interface {
		Ping(ctx context.Context) error
	}
	StartTime time.Time
	Build     BuildInfo
	// ProxyHandler 处理 /v2/* 请求（registry 反代）；nil 时 /v2/* 返回 503。
	ProxyHandler http.Handler
	// ResourceHandler 处理 /r/* 资源加速反代（PRD §9.4）；nil 时 /r/* 返回 503。
	ResourceHandler http.Handler
	// AccessLogWriter 写访问日志；nil 时不记。
	AccessLogWriter AccessLogWriter
	// GetUpstreams 列出 enabled upstreams（api/dashboard 用）。
	GetUpstreams func(ctx context.Context) ([]Upstream, error)
	// GetDashboardSummary 聚合仪表盘数据。
	GetDashboardSummary func(ctx context.Context) (DashboardSummary, error)
	// GetAccessLogs 分页查询 request_logs（支持筛选）。
	GetAccessLogs func(ctx context.Context, page, pageSize int, filter LogFilter) ([]AccessLogRecord, int, error)
	// PurgeAccessLogs 删除指定时间之前的日志，返回删除行数。
	PurgeAccessLogs func(ctx context.Context, before int64) (int64, error)
	// CountAccessLogs 返回日志总行数。
	CountAccessLogs func(ctx context.Context) (int, error)
	// GetCacheEntries 分页查询 cache_entries。
	GetCacheEntries func(ctx context.Context, page, pageSize int, query string) ([]CacheEntry, int, error)
	// DeleteCacheEntry 按 id 删除（DB 行 + 调用方负责删 blob 文件）。
	DeleteCacheEntry func(ctx context.Context, id int64) error
	// ListCleanupTasks 列出所有 cleanup_tasks。
	ListCleanupTasks func(ctx context.Context) ([]CleanupTask, error)
	// RunCleanupTask 跑一次指定 id 的清理；返回报告。
	RunCleanupTask func(ctx context.Context, id int64) (CleanupReport, error)
	// GetUpstreamHealth 拿上游连通性快照。
	GetUpstreamHealth func() UpstreamHealth
	// GetSettings 拿系统设置。
	GetSettings func(ctx context.Context) (SystemSettings, error)
	// UpdateSettings 改系统设置。
	UpdateSettings func(ctx context.Context, patch SettingsPatch, userID int64) (SystemSettings, error)
	// DryRunCleanup 跑一次清理预估（不实际删除）。
	DryRunCleanup func(ctx context.Context, taskID int64) (CleanupReport, error)
	// ListRegistries 列出所有 registry upstreams（含凭据状态标志，§9.7.3）。
	ListRegistries func(ctx context.Context) ([]storage.Registry, error)
	// SetRegistryEnabled 启停 registry upstream。
	SetRegistryEnabled func(ctx context.Context, name string, enabled bool) error
	// SetRegistryCredentials 写上游凭据（§9.7.3）。
	SetRegistryCredentials func(ctx context.Context, name string, patch storage.RegistryCredentialsPatch) error
	// CredentialCipher 提供 AES-256-GCM 加解密（master key 从 main.go 注入）。
	CredentialCipher Cipher
	// AuthDB 鉴权后端（PRD §9.7.1）。nil 时不要求登录（开发模式）。
	AuthDB AuthDB
	// SessionUserRole 返回当前 session 用户的角色（"admin"/"user"/""）。nil = 未登录。
	SessionUserRole func(ctx context.Context, r *http.Request) (string, int64, error)
	// GetDNSConfig 拿 DNS 启动器配置（PRD §9.3）。
	GetDNSConfig func(ctx context.Context) (storage.DNSConfig, error)
	// UpdateDNSConfig 改 DNS 启动器配置。
	UpdateDNSConfig func(ctx context.Context, patch storage.DNSConfigPatch) (storage.DNSConfig, error)
	// DNSServer 内置 mini DNS server 实例（PRD §9.3）。
	DNSServer *dnsserver.Server
	// SteamCMD AppID CRUD（PRD §9.3.3）
	ListSteamAppIDs     func(ctx context.Context) ([]storage.SteamAppID, error)
	GetSteamAppID       func(ctx context.Context, id int64) (storage.SteamAppID, error)
	CreateSteamAppID    func(ctx context.Context, in storage.SteamAppID) (storage.SteamAppID, error)
	UpdateSteamAppID    func(ctx context.Context, id int64, patch storage.SteamAppIDPatch) (storage.SteamAppID, error)
	DeleteSteamAppID    func(ctx context.Context, id int64) error
	RecordPreheatResult func(ctx context.Context, id int64, status, message string, durationMs int64) error
	// 通用预热任务（PRD §9.2.3 / §9.5.5）
	ListPreheatTasks   func(ctx context.Context) ([]storage.PreheatTask, error)
	GetPreheatTask     func(ctx context.Context, id int64) (storage.PreheatTask, error)
	CreatePreheatTask  func(ctx context.Context, in storage.PreheatTask) (storage.PreheatTask, error)
	DeletePreheatTask  func(ctx context.Context, id int64) error
	ListPreheatItems   func(ctx context.Context, taskID int64) ([]storage.PreheatItem, error)
	RunPreheatTask     func(ctx context.Context, id int64) error
	CancelPreheatTask  func(id int64) bool
	// 诊断中心（PRD §9.7）
	RunDiagnostics func(ctx context.Context) diagnostics.FullReport
	// 资源加速中心（PRD §9.4）
	ListResourceRules        func(ctx context.Context) ([]storage.ResourceRule, error)
	CreateResourceRule       func(ctx context.Context, in storage.ResourceRule) (storage.ResourceRule, error)
	UpdateResourceRule       func(ctx context.Context, id int64, patch storage.ResourceRulePatch) (storage.ResourceRule, error)
	DeleteResourceRule       func(ctx context.Context, id int64) error
	ListResourceCache        func(ctx context.Context, ruleID int64, limit int) ([]storage.ResourceCacheEntry, error)
	DeleteResourceCacheEntry func(ctx context.Context, id int64) error
	// Prometheus metrics 注入（P2#2）
	MetricsDB         interface {
		DashboardSummary(ctx context.Context) (storage.DashboardSummary, error)
		ListResourceRules(ctx context.Context) ([]storage.ResourceRule, error)
		ResourceStats(ctx context.Context) (storage.ResourceStatsSummary, error)
	}
	MetricsDNSServer interface {
		Stats() dnsserver.Stats
	}
	MetricsUpstreams func() []metrics.UpstreamStatus
	MetricsVersion   string
	MetricsCommit    string
	MetricsStartTime time.Time
	// 诊断包导出（P2#3）— 注入 BundleSource，handler 直接调 diagnostics.WriteBundle。
	BundleSource diagnostics.BundleSource
	// 代理访问控制（P2#4 / PRD §9.7.2）— 给 /v2/* 和 /r/* 用。
	// nil 表示完全开放（默认）。
	AccessControlResolve access.Resolver
	// AccessControlReload 重新从 DB 读最新配置（写 DB 后由 PUT handler 调）。
	AccessControlReload func()
	// LoginRateLimiter 限制登录端点的请求频率（防暴力破解，PRD §15.3）。
	// nil 时不限制。
	LoginRateLimiter *ratelimit.Limiter
	// APIRateLimiter 限制通用 API 写操作的请求频率（防 DoS）。
	// nil 时不限制。
	APIRateLimiter *ratelimit.Limiter
	// PublicBaseURL 客户端可访问的 CNCH 公开地址（admin 在 SettingsView 配）。
	// 每次调用读最新值（main.go 注入的 closure 实现 hot-reload）。
	// 返回空字符串 = fallback 到 r.Host。
	PublicBaseURL func() string
}


// Upstream 是 /api/docker/upstreams 返回的列表项。
type Upstream struct {
	ID          int64  `json:"id"`
	Name        string `json:"name"`
	UpstreamURL string `json:"upstreamUrl"`
	MirrorPath  string `json:"mirrorPath"`
	Enabled     bool   `json:"enabled"`
}

// DashboardSummary 是 /api/dashboard/summary 返回的聚合。
type DashboardSummary struct {
	CacheEntries    int   `json:"cacheEntries"`
	CacheBytes      int64 `json:"cacheBytes"`
	CacheHits       int64 `json:"cacheHits"`
	BypassedCount   int   `json:"bypassedCount"`
	HitCount        int64 `json:"hitCount"`
	MissCount       int64 `json:"missCount"`
	RequestCount24h int   `json:"requestCount24h"`
	ErrorCount24h   int   `json:"errorCount24h"`
	BytesOut24h     int64 `json:"bytesOut24h"`
	ActiveUpstreams int   `json:"activeUpstreams"`
	GeneratedAt     int64 `json:"generatedAt"`
}

// CacheEntry 是 /api/cache/entries 列表项。
type CacheEntry struct {
	ID           int64  `json:"id"`
	Registry     string `json:"registry"`
	Repository   string `json:"repository"`
	Digest       string `json:"digest"`
	MediaType    string `json:"mediaType"`
	SizeBytes    int64  `json:"sizeBytes"`
	StoragePath  string `json:"storagePath"`
	HitCount     int    `json:"hitCount"`
	LastAccessAt int64  `json:"lastAccessAt"`
	CreatedAt    int64  `json:"createdAt"`
	Bypassed     bool   `json:"bypassed"`
	BypassReason string `json:"bypassReason"`
}

// CleanupTask 是 /api/cleanup/tasks 列表项。
type CleanupTask struct {
	ID               int64  `json:"id"`
	Name             string `json:"name"`
	Strategy         string `json:"strategy"`
	ThresholdSeconds int    `json:"thresholdSeconds"`
	ThresholdBytes   int64  `json:"thresholdBytes"`
	Enabled          bool   `json:"enabled"`
	CronIntervalSec  int    `json:"cronIntervalSec"`
	LastRunAt        int64  `json:"lastRunAt"`
	LastStatus       string `json:"lastStatus"`
	LastFreedBytes   int64  `json:"lastFreedBytes"`
	LastFreedCount   int    `json:"lastFreedCount"`
	CreatedAt        int64  `json:"createdAt"`
}

// CleanupReport 是清理一次的结果。
type CleanupReport struct {
	TaskID      int64  `json:"taskId"`
	Strategy    string `json:"strategy"`
	FreedCount  int    `json:"freedCount"`
	FreedBytes  int64  `json:"freedBytes"`
	BeforeCount int    `json:"beforeCount"`
	BeforeBytes int64  `json:"beforeBytes"`
	AfterCount  int    `json:"afterCount"`
	AfterBytes  int64  `json:"afterBytes"`
	DurationMs  int64  `json:"durationMs"`
}

// UpstreamHealth 是 /api/health/upstream 响应。
type UpstreamHealth struct {
	URL         string `json:"url"`
	Reachable   bool   `json:"reachable"`
	LatencyMs   int64  `json:"latencyMs"`
	Error       string `json:"error,omitempty"`
	LastChecked int64  `json:"lastChecked"`
}

// SystemSettings 是 /api/settings 响应 / 入参。
type SystemSettings struct {
	SmallVPSOpt       bool   `json:"smallVpsOpt"`
	ReserveSpaceGB    int    `json:"reserveSpaceGb"`
	MaxObjectSizeMB   int    `json:"maxObjectSizeMb"`
	CacheTotalGB      int    `json:"cacheTotalGb"`
	CleanupTriggerPct int    `json:"cleanupTriggerPct"`
	CleanupTargetPct  int    `json:"cleanupTargetPct"`
	PublicBaseURL     string `json:"publicBaseUrl"`
	LogRetentionDays  int    `json:"logRetentionDays"` // 0 = 不自动清理
	UpdatedAt         int64  `json:"updatedAt"`
}

// SettingsPatch 是 PATCH /api/settings 入参（所有字段可选）。
type SettingsPatch struct {
	SmallVPSOpt       *bool   `json:"smallVpsOpt,omitempty"`
	ReserveSpaceGB    *int    `json:"reserveSpaceGb,omitempty"`
	MaxObjectSizeMB   *int    `json:"maxObjectSizeMb,omitempty"`
	CacheTotalGB      *int    `json:"cacheTotalGb,omitempty"`
	CleanupTriggerPct *int    `json:"cleanupTriggerPct,omitempty"`
	CleanupTargetPct  *int    `json:"cleanupTargetPct,omitempty"`
	PublicBaseURL     *string `json:"publicBaseUrl,omitempty"`
	LogRetentionDays  *int    `json:"logRetentionDays,omitempty"`
}

// RegistryPatch 是 PATCH /api/registries/:name 入参。
//
// §9.7.3 扩展：除了 enabled 还可以传 username / password / token 凭据。
// password / token 在 handler 内加密后存；空字符串不更新；显式 clearPassword/clearToken 清空。
type RegistryPatch struct {
	Enabled       *bool   `json:"enabled,omitempty"`
	Username      *string `json:"username,omitempty"`
	Password      *string `json:"password,omitempty"` // 明文，handler 加密
	Token         *string `json:"token,omitempty"`    // 明文，handler 加密
	ClearPassword bool    `json:"clearPassword,omitempty"`
	ClearToken    bool    `json:"clearToken,omitempty"`
}

// NewRouter 构造配置好的 chi 路由。
//
// 返回的 Router 已经是中间件齐备的实例，可以直接挂到 http.Server。
func NewRouter(opts Options) http.Handler {
	r := chi.NewRouter()

	// 全局 middleware。
	// 注意：自己实现 requestIDMiddleware 而不是用 chimw.RequestID，
	// 因为后者只把 ID 写进 context，不写到响应头 —— 而我们要支持
	// 跨服务 trace 时透出 X-Request-Id。
	r.Use(requestIDMiddleware())
	r.Use(chimw.RealIP)
	r.Use(loggerMiddleware())
	r.Use(chimw.Recoverer)
	r.Use(jsonContentTypeMiddleware())
	// 通用 API 限流（防 DoS，PRD §15.3）。
	if opts.APIRateLimiter != nil {
		r.Use(rateLimitMiddleware(opts.APIRateLimiter, nil))
	}
	// 鉴权放在最后（在 logger 后），这样 401 也会被 logger 记录。
	r.Use(requireAuth(opts))

	// 健康检查（同时挂在 / 与 /api 下）。
	r.Get("/healthz", rootHealthHandler())
	r.Route("/api", func(r chi.Router) {
		// 公开
		r.Get("/healthz", apiHealthHandler(opts))
		r.Get("/version", versionHandler(opts.Build))
		r.Route("/auth", func(r chi.Router) {
			r.Get("/init-status", initStatusHandler(opts))
			r.Post("/init", initHandler(opts))
			// 登录端点独立限流（防暴力破解，比通用 API 更严格）。
			if opts.LoginRateLimiter != nil {
				r.With(rateLimitMiddleware(opts.LoginRateLimiter, nil)).Post("/login", loginHandler(opts))
			} else {
				r.Post("/login", loginHandler(opts))
			}
			r.Post("/logout", logoutHandler(opts))
			r.Get("/me", meHandler(opts))
			r.Post("/change-password", changePasswordHandler(opts))
		})
		// 受保护（requireAuth 拦截）
		r.Get("/docker/upstreams", upstreamsHandler(opts))
		r.Get("/docker/daemon.json", daemonJSONHandler(opts))
		r.Get("/cache/entries", cacheEntriesHandler(opts))
		r.Delete("/cache/entries/{id}", cacheDeleteHandler(opts))
		r.Get("/logs", accessLogsHandler(opts))
		r.Delete("/logs", purgeLogsHandler(opts))
		r.Get("/logs/stats", logStatsHandler(opts))
		r.Get("/dashboard/summary", dashboardSummaryHandler(opts))
		r.Get("/cleanup/tasks", cleanupTasksHandler(opts))
		r.Post("/cleanup/tasks/{id}/run", runCleanupHandler(opts))
		r.Get("/health/upstream", upstreamHealthHandler(opts))
		r.Get("/settings", settingsGetHandler(opts))
		r.Patch("/settings", settingsPatchHandler(opts))
		r.Post("/cleanup/tasks/{id}/dry-run", cleanupDryRunHandler(opts))
		r.Get("/registries", registriesListHandler(opts))
		r.Patch("/registries/{name}", registryPatchHandler(opts))
		r.Post("/client-config", generateClientConfigHandler(opts))
		r.Post("/client-config/bundle", generateClientConfigBundleHandler(opts)) // §9.5.4
		// SteamCMD DNS 启动器（PRD §9.3）
		r.Get("/dns/config", dnsConfigGetHandler(opts))
		r.Patch("/dns/config", dnsConfigPatchHandler(opts))
		r.Get("/dns/stats", dnsStatsHandler(opts))
		r.Get("/dns/test", dnsTestHandler(opts))
		r.Post("/dns/test", dnsTestHandler(opts))
		// SteamCMD AppID 管理（PRD §9.3.3）
		r.Get("/steamcmd/appids", steamAppIDListHandler(opts))
		r.Post("/steamcmd/appids", steamAppIDCreateHandler(opts))
		r.Patch("/steamcmd/appids/{id}", steamAppIDPatchHandler(opts))
		r.Delete("/steamcmd/appids/{id}", steamAppIDDeleteHandler(opts))
		r.Post("/steamcmd/appids/{id}/preheat", steamAppIDPreheatHandler(opts))
		// 通用预热任务（PRD §9.2.3 / §9.5.5）
		r.Get("/preheat/tasks", preheatTaskListHandler(opts))
		r.Post("/preheat/tasks", preheatTaskCreateHandler(opts))
		r.Delete("/preheat/tasks/{id}", preheatTaskDeleteHandler(opts))
		r.Post("/preheat/tasks/{id}/run", preheatTaskRunHandler(opts))
		r.Post("/preheat/tasks/{id}/cancel", preheatTaskCancelHandler(opts))
		r.Get("/preheat/tasks/{id}/items", preheatTaskItemsHandler(opts))
		// 诊断中心（PRD §9.7）
		r.Get("/diagnostics/run", diagnosticsRunHandler(opts))
		r.Post("/diagnostics/bundle", diagnosticsBundleHandler(opts)) // P2#3
		// 资源加速中心（PRD §9.4）
		r.Get("/resources/rules", resourceRuleListHandler(opts))
		r.Post("/resources/rules", resourceRuleCreateHandler(opts))
		r.Patch("/resources/rules/{id}", resourceRulePatchHandler(opts))
		r.Delete("/resources/rules/{id}", resourceRuleDeleteHandler(opts))
		r.Get("/resources/rules/{id}/cache", resourceCacheListHandler(opts))
		r.Delete("/resources/cache/{id}", resourceCacheDeleteHandler(opts))
		r.Get("/resources/templates", resourceTemplatesHandler(opts)) // P2#1
		// 代理访问控制（P2#4 / PRD §9.7.2）
		r.Get("/access-control", accessControlGetHandler(opts))
		r.Put("/access-control", accessControlPutHandler(opts))
	})

	// Prometheus metrics 端点（P2#2）— 公开（Prometheus scrape 习惯）
	r.Get("/metrics", metricsHandler(opts))

	// /v2/* — 镜像反代
	if opts.ProxyHandler != nil {
		var v2Chain chi.Router = r
		if opts.AccessControlResolve != nil {
			v2Chain = v2Chain.With(access.Middleware(opts.AccessControlResolve))
		}
		v2Chain.Handle("/v2", opts.ProxyHandler)
		v2Chain.Handle("/v2/*", opts.ProxyHandler)
	}

	// /r/* — 资源加速反代（PRD §9.4）
	if opts.ResourceHandler != nil {
		var rChain chi.Router = r
		if opts.AccessControlResolve != nil {
			rChain = rChain.With(access.Middleware(opts.AccessControlResolve))
		}
		rChain.Handle("/r", opts.ResourceHandler)
		rChain.Handle("/r/*", opts.ResourceHandler)
	} else {
		// 注入缺失：返回 503
		r.Get("/v2", func(w http.ResponseWriter, r *http.Request) {
			writeError(w, http.StatusServiceUnavailable, "proxy_disabled", "registry proxy is not configured")
		})
		r.Get("/v2/*", func(w http.ResponseWriter, r *http.Request) {
			writeError(w, http.StatusServiceUnavailable, "proxy_disabled", "registry proxy is not configured")
		})
	}

	// 兜底 404。
	r.NotFound(func(w http.ResponseWriter, r *http.Request) {
		writeError(w, http.StatusNotFound, "not_found", "route not found: "+r.Method+" "+r.URL.Path)
	})

	// 兜底 405。
	r.MethodNotAllowed(func(w http.ResponseWriter, r *http.Request) {
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed: "+r.Method+" "+r.URL.Path)
	})

	return r
}

// loggerMiddleware 用我们自己的 log 包记录每个请求的 method/path/status/duration。
func loggerMiddleware() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			ww := chimw.NewWrapResponseWriter(w, r.ProtoMajor)
			next.ServeHTTP(ww, r)
			dur := time.Since(start)
			reqID := chimw.GetReqID(r.Context())
			logpkg.Info("http",
				"request_id", reqID,
				"method", r.Method,
				"path", r.URL.Path,
				"status", ww.Status(),
				"bytes", ww.BytesWritten(),
				"duration_ms", dur.Milliseconds(),
				"remote", r.RemoteAddr,
			)
		})
	}
}

// requestIDMiddleware 自己实现 request id 处理。
//
// 行为：
//   - 优先使用请求头 X-Request-Id（让上游 / 客户端能注入 trace id）；
//   - 否则用本地计数器 + crypto/rand 生成一个；
//   - 把 id 写进 context（key = chimw.RequestIDKey，让 chimw.GetReqID 仍可读）；
//   - 同时把 id 写到响应头 X-Request-Id，便于客户端排障。
func requestIDMiddleware() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			id := r.Header.Get("X-Request-Id")
			if id == "" {
				id = generateRequestID()
			}
			ctx := context.WithValue(r.Context(), chimw.RequestIDKey, id)
			w.Header().Set("X-Request-Id", id)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

var (
	reqIDPrefix  string
	reqIDCounter atomic.Uint64
)

// generateRequestID 生成本进程内的唯一 ID。
// 形式：<host>/<10-char-base64>-<6-digit-counter>
func generateRequestID() string {
	if reqIDPrefix == "" {
		// 初始化一次：hostname + 随机串。
		hostname, _ := os.Hostname()
		if hostname == "" {
			hostname = "localhost"
		}
		var buf [12]byte
		var b64 string
		for len(b64) < 10 {
			_, _ = rand.Read(buf[:])
			b64 = base64.StdEncoding.EncodeToString(buf[:])
			b64 = strings.NewReplacer("+", "", "/", "").Replace(b64)
		}
		reqIDPrefix = hostname + "/" + b64[0:10]
	}
	n := reqIDCounter.Add(1)
	return fmt.Sprintf("%s-%06d", reqIDPrefix, n)
}

// jsonContentTypeMiddleware 默认所有响应 Content-Type 为 application/json。
// 注意：仅对未显式设置 Content-Type 的响应生效。
func jsonContentTypeMiddleware() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ww := chimw.NewWrapResponseWriter(w, r.ProtoMajor)
			ww.Header().Set("Content-Type", "application/json; charset=utf-8")
			next.ServeHTTP(ww, r)
		})
	}
}

// rateLimitMiddleware 按 IP 做 token-bucket 限流。
//
// limiter 不可为 nil（调用方负责判 nil 再挂）。
// skipPath 可选：匹配的路径跳过限流（如健康检查）。nil 表示全限。
// 被限流时返 429 + Retry-After 头。
func rateLimitMiddleware(limiter *ratelimit.Limiter, skipPath func(path string) bool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if skipPath != nil && skipPath(r.URL.Path) {
				next.ServeHTTP(w, r)
				return
			}
			key := clientIP(r)
			ok, retryAfter := limiter.Take(key)
			if !ok {
				w.Header().Set("Retry-After", fmt.Sprintf("%.0f", retryAfter.Seconds()+1))
				writeError(w, http.StatusTooManyRequests, "rate_limited", "too many requests, try again later")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// writeJSON 通用 JSON 响应。
func writeJSON(w http.ResponseWriter, status int, body any) {
	if err := encodeJSON(w, status, body); err != nil {
		logpkg.Error("write json response", "err", err.Error())
	}
}

// writeError 输出统一格式的错误响应。
//
// shape: {"error":{"code":"...","message":"..."}}
func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]any{
		"error": map[string]any{
			"code":    code,
			"message": message,
		},
	})
}

// writeInternalErr 5xx 错误的统一处理：log 详细 err，返 generic message。
//
// 5xx（500/502/503 等）错误可能含敏感信息（SQL 错误、文件路径、内部状态），
// 不应该直接返给客户端。详细 err 走 log（结构化 + 含 request_id 便于追踪），
// 客户端只看到 "internal error" 跟一个稳定的 code。
//
// 用法：writeInternalErr(w, r, "session_create_failed", err)
func writeInternalErr(w http.ResponseWriter, r *http.Request, code string, err error) {
	reqID := chimw.GetReqID(r.Context())
	logpkg.Error("api internal error",
		"request_id", reqID,
		"code", code,
		"err", err.Error(),
	)
	writeError(w, http.StatusInternalServerError, code, "internal error")
}

// httpError 是 handler 内部可选用的 error 类型：携带 HTTP status。
type httpError struct {
	Status  int
	Code    string
	Message string
}

func (e *httpError) Error() string { return e.Message }

func newHTTPError(status int, code, message string) *httpError {
	return &httpError{Status: status, Code: code, Message: message}
}

// asHTTPError 尝试把 error 还原为 *httpError。
func asHTTPError(err error) (*httpError, bool) {
	var he *httpError
	if errors.As(err, &he) {
		return he, true
	}
	return nil, false
}
