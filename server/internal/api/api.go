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

// AccessLogRecord 是 /v2/* 访问日志的简版（与 storage.AccessLogRecord 同形但解耦）。
//
// api 包不直接 import storage（避免循环），所以这里独立定义 record 类型。
// main.go 注入 AccessLogWriter 时负责转换。
type AccessLogRecord struct {
	Method     string
	Path       string
	Status     int
	DurationMs int64
	Cached     bool
	Bypassed   bool
	ClientIP   string
	Bytes      int64
	Error      string
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
	// AccessLogWriter 写访问日志；nil 时不记。
	AccessLogWriter AccessLogWriter
	// GetUpstreams 列出 enabled upstreams（api/dashboard 用）。
	GetUpstreams func(ctx context.Context) ([]Upstream, error)
	// GetDashboardSummary 聚合仪表盘数据。
	GetDashboardSummary func(ctx context.Context) (DashboardSummary, error)
	// GetAccessLogs 分页查询 request_logs。
	GetAccessLogs func(ctx context.Context, page, pageSize int) ([]AccessLogRecord, int, error)
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
	HitCount        int64 `json:"hitCount"`
	MissCount       int64 `json:"missCount"`
	RequestCount24h int   `json:"requestCount24h"`
	ErrorCount24h   int   `json:"errorCount24h"`
	BytesOut24h     int64 `json:"bytesOut24h"`
	ActiveUpstreams int   `json:"activeUpstreams"`
	GeneratedAt     int64 `json:"generatedAt"`
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

	// 健康检查（同时挂在 / 与 /api 下）。
	r.Get("/healthz", rootHealthHandler())
	r.Route("/api", func(r chi.Router) {
		r.Get("/healthz", apiHealthHandler(opts))
		r.Get("/version", versionHandler(opts.Build))
		r.Get("/docker/upstreams", upstreamsHandler(opts))
		r.Get("/docker/daemon.json", daemonJSONHandler(opts))
		r.Get("/cache/entries", cacheEntriesHandler(opts))
		r.Get("/logs", accessLogsHandler(opts))
		r.Get("/dashboard/summary", dashboardSummaryHandler(opts))
	})

	// /v2/* — 镜像反代
	if opts.ProxyHandler != nil {
		r.Handle("/v2", opts.ProxyHandler)
		r.Handle("/v2/*", opts.ProxyHandler)
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
