// Command cncachehub 是 CNCacheHub 的入口程序（Phase 1：Docker Hub pull-through cache）。
//
// 启动流程：
//  1. 加载配置（config.Load）—— 校验失败立即退出；
//  2. 初始化日志（slog JSON + 敏感字段脱敏）；
//  3. 打开 SQLite + 跑 migrations；
//  4. 初始化 cache（FileStore）+ 上游（Upstream）+ Proxy；
//  5. 启 access log consumer goroutine（channel → DB）；
//  6. 启动 HTTP server（chi 路由 + /v2/* 挂到 proxy）；
//  7. 监听 SIGINT / SIGTERM，收到后 graceful shutdown（30s 超时）。
//
// 不做的事（明确边界）：
//   - 不做 Www-Authenticate token dance（Phase 1.1）；
//   - 不做鉴权 / 登录；
//   - 不做预热 / 清理 / 通知；
//   - 不 panic 处理运行时错误（任何失败都返回 error 并优雅退出）。
package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"runtime"
	"syscall"
	"time"

	"github.com/cncachehub/server/internal/api"
	"github.com/cncachehub/server/internal/cache"
	"github.com/cncachehub/server/internal/config"
	logpkg "github.com/cncachehub/server/internal/log"
	"github.com/cncachehub/server/internal/proxy"
	"github.com/cncachehub/server/internal/storage"
)

// 编译期可由 -ldflags 覆盖的变量。
var (
	version = "dev"
	commit  = "local"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "fatal: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	// 1. 配置。
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	// 2. 日志。
	cfg.InitLogger()
	logpkg.Info("config loaded",
		"http_addr", cfg.HTTPAddr,
		"data_dir", cfg.DataDir,
		"db_path", cfg.DBPath,
		"cache_dir", cfg.CacheDir,
		"upstream_registry", cfg.UpstreamRegistry,
		"small_vps_opt", cfg.SmallVPSOpt,
		"reserve_space_gb", cfg.ReserveSpaceGB,
		"max_object_size_mb", cfg.MaxObjectSizeMB,
		"cache_total_gb", cfg.CacheTotalGB,
		"log_level", cfg.LogLevel,
		"shutdown_timeout_seconds", int(cfg.ShutdownTimeout.Seconds()),
		"admin_password", cfg.AdminPassword, // 由 redactingHandler 脱敏为 "***"
	)
	logpkg.Info("cncachehub starting", "version", version, "go", runtime.Version())

	// 3. 打开存储。
	rootCtx, rootCancel := context.WithCancel(context.Background())
	defer rootCancel()

	db, err := storage.Open(rootCtx, cfg.DataDir)
	if err != nil {
		return fmt.Errorf("open storage: %w", err)
	}
	defer func() {
		if err := db.Close(); err != nil {
			logpkg.Warn("close storage", "err", err.Error())
		}
	}()
	logpkg.Info("storage ready", "db_path", db.Path)

	// 4. 初始化 cache + 上游 + proxy。
	fs, err := cache.NewFileStore(cfg.CacheDir, cache.Policy{
		MaxObjectSize: int64(cfg.MaxObjectSizeMB) * 1024 * 1024,
		ReserveSpace:  int64(cfg.ReserveSpaceGB) * 1024 * 1024 * 1024,
	})
	if err != nil {
		return fmt.Errorf("init cache: %w", err)
	}
	logpkg.Info("cache ready", "root", fs.RootDir())

	up, err := proxy.NewUpstream(proxy.UpstreamOptions{
		BaseURL: cfg.UpstreamRegistry,
		Timeout: cfg.UpstreamTimeout,
		UA:      fmt.Sprintf("cncachehub/%s", version),
	})
	if err != nil {
		return fmt.Errorf("init upstream: %w", err)
	}

	// access log channel：proxy 写，主 goroutine 消费 + 落 DB。
	accessLogCh := make(chan proxy.AccessLog, 1000)
	proxyHandler := proxy.New(fs, up, accessLogCh)
	logpkg.Info("proxy ready", "upstream", cfg.UpstreamRegistry)

	// 5. 启 access log consumer goroutine。
	go consumeAccessLogs(rootCtx, db, accessLogCh)

	// 6. 构造 HTTP server。
	build := api.BuildInfo{
		Name:    "cncachehub",
		Version: version,
		Go:      runtime.Version(),
		Commit:  commit,
	}
	handler := api.NewRouter(api.Options{
		DB:                  db,
		StartTime:           cfg.StartTime,
		Build:               build,
		ProxyHandler:        proxyHandler,
		AccessLogWriter:     &accessLogBridge{db: db},
		GetUpstreams:        makeUpstreamsAdapter(db),
		GetDashboardSummary: makeDashboardAdapter(db),
		GetAccessLogs:       makeListAccessLogsAdapter(db),
	})
	srv := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      0, // 流式下载需要无写超时
		IdleTimeout:       120 * time.Second,
	}

	// 7. 启动 + 信号处理。
	errCh := make(chan error, 1)
	go func() {
		logpkg.Info("listening on " + cfg.HTTPAddr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
		close(errCh)
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGINT, syscall.SIGTERM)

	select {
	case sig := <-stop:
		logpkg.Info("shutdown signal received", "signal", sig.String())
	case err, ok := <-errCh:
		if ok && err != nil {
			return fmt.Errorf("http server: %w", err)
		}
	}

	// 8. Graceful shutdown。
	rootCancel() // 通知 access log consumer 退出
	shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("http shutdown: %w", err)
	}
	logpkg.Info("http server stopped")
	return nil
}

// consumeAccessLogs 从 channel 收 access log + 写 DB。
//
// 行为：
//   - 100ms 批量 flush，提升吞吐；
//   - channel 关闭 / ctx 取消时退出。
func consumeAccessLogs(ctx context.Context, db *storage.DB, ch <-chan proxy.AccessLog) {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	var (
		batch   []storage.AccessLogRecord
		batchN  int
	)
	flush := func() {
		if len(batch) == 0 {
			return
		}
		wctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		for _, rec := range batch {
			if err := db.InsertAccessLog(wctx, rec); err != nil {
				logpkg.Warn("insert access log", "err", err.Error())
			}
		}
		batch = batch[:0]
		batchN = 0
	}

	for {
		select {
		case <-ctx.Done():
			// drain + flush
			for {
				select {
				case rec, ok := <-ch:
					if !ok {
						flush()
						return
					}
					batch = append(batch, proxyToStorageRec(rec))
				default:
					flush()
					return
				}
			}
		case rec, ok := <-ch:
			if !ok {
				flush()
				return
			}
			batch = append(batch, proxyToStorageRec(rec))
			batchN++
			if batchN >= 50 {
				flush()
			}
		case <-ticker.C:
			flush()
		}
	}
}

// proxyToStorageRec 把 proxy.AccessLog 转成 storage.AccessLogRecord。
func proxyToStorageRec(rec proxy.AccessLog) storage.AccessLogRecord {
	return storage.AccessLogRecord{
		CreatedAt:  time.Now().Unix(),
		Method:     rec.Method,
		Path:       rec.Path,
		Status:     rec.Status,
		DurationMs: rec.DurationMs,
		Cached:     rec.Cached,
		Bypassed:   rec.Bypassed != cache.BypassNone,
		ClientIP:   rec.ClientIP,
		Bytes:      rec.Bytes,
		Error:      rec.Error,
	}
}

// accessLogBridge 把 api.AccessLogRecord 写入 storage。
//
// 实际不会调用（因为 access log 由 proxy 直接发给 main 的 consumer），
// 保留以满足 api.AccessLogWriter 接口。
type accessLogBridge struct {
	db *storage.DB
}

func (a *accessLogBridge) WriteAccessLog(ctx context.Context, rec api.AccessLogRecord) error {
	return a.db.InsertAccessLog(ctx, storage.AccessLogRecord{
		CreatedAt:  time.Now().Unix(),
		Method:     rec.Method,
		Path:       rec.Path,
		Status:     rec.Status,
		DurationMs: rec.DurationMs,
		Cached:     rec.Cached,
		Bypassed:   rec.Bypassed,
		ClientIP:   rec.ClientIP,
		Bytes:      rec.Bytes,
		Error:      rec.Error,
	})
}

// makeUpstreamsAdapter 包装 storage.ListEnabledUpstreams → api.Upstream。
func makeUpstreamsAdapter(db *storage.DB) func(ctx context.Context) ([]api.Upstream, error) {
	return func(ctx context.Context) ([]api.Upstream, error) {
		rows, err := db.ListEnabledUpstreams(ctx)
		if err != nil {
			return nil, err
		}
		out := make([]api.Upstream, 0, len(rows))
		for _, r := range rows {
			out = append(out, api.Upstream{
				ID:          r.ID,
				Name:        r.Name,
				UpstreamURL: r.UpstreamURL,
				MirrorPath:  r.MirrorPath,
				Enabled:     r.Enabled,
			})
		}
		return out, nil
	}
}

// makeDashboardAdapter 包装 storage.DashboardSummary → api.DashboardSummary。
func makeDashboardAdapter(db *storage.DB) func(ctx context.Context) (api.DashboardSummary, error) {
	return func(ctx context.Context) (api.DashboardSummary, error) {
		s, err := db.DashboardSummary(ctx)
		if err != nil {
			return api.DashboardSummary{}, err
		}
		return api.DashboardSummary{
			CacheEntries:    s.CacheEntries,
			CacheBytes:      s.CacheBytes,
			HitCount:        s.HitCount,
			MissCount:       s.MissCount,
			RequestCount24h: s.RequestCount24h,
			ErrorCount24h:   s.ErrorCount24h,
			BytesOut24h:     s.BytesOut24h,
			ActiveUpstreams: s.ActiveUpstreams,
			GeneratedAt:     s.GeneratedAt,
		}, nil
	}
}

// makeListAccessLogsAdapter 返回闭包，把 storage.AccessLogRecord 转为 api.AccessLogRecord。
func makeListAccessLogsAdapter(db *storage.DB) func(ctx context.Context, page, pageSize int) ([]api.AccessLogRecord, int, error) {
	return func(ctx context.Context, page, pageSize int) ([]api.AccessLogRecord, int, error) {
		rows, total, err := db.ListAccessLogs(ctx, page, pageSize)
		if err != nil {
			return nil, 0, err
		}
		out := make([]api.AccessLogRecord, 0, len(rows))
		for _, r := range rows {
			out = append(out, api.AccessLogRecord{
				Method:     r.Method,
				Path:       r.Path,
				Status:     r.Status,
				DurationMs: r.DurationMs,
				Cached:     r.Cached,
				Bypassed:   r.Bypassed,
				ClientIP:   r.ClientIP,
				Bytes:      r.Bytes,
				Error:      r.Error,
			})
		}
		return out, total, nil
	}
}
