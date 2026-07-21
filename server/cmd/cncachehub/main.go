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
//   - 不做预热 / 通知；
//   - 不 panic 处理运行时错误（任何失败都返回 error 并优雅退出）。
//
// 已做：
//   - 控制台登录（PRD §9.7.1）：cookie session + bcrypt + 审计日志。
package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"runtime"
	"sync"
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
	// metaWriter adapter：proxy.MetaWriter 接口 → storage.DB。
	metaAdapter := &metaWriterAdapter{db: db}
	proxyHandler := proxy.New(fs, up, accessLogCh, metaAdapter)
	logpkg.Info("proxy ready", "upstream", cfg.UpstreamRegistry)

	// 5. 启 access log consumer goroutine。
	go consumeAccessLogs(rootCtx, db, accessLogCh)

	// 5b. 启 cleanup scheduler goroutine（按 cron 跑 LRU / capacity 清理）。
	go runCleanupScheduler(rootCtx, db, fs, cfg.CacheTotalGB)

	// 5c. 启 upstream health checker（每 60s 探一次，缓存在内存里给 /api/health/upstream 读）。
	upstreamHealth := startUpstreamHealthChecker(rootCtx, cfg.UpstreamRegistry)

	// 5d. 启 session cleanup goroutine（每小时清过期 session）。
	go runSessionCleanup(rootCtx, db)

	// 5e. 检查是否首次启动（无 admin），提醒用户初始化。
	if n, _ := db.CountUsers(rootCtx); n == 0 {
		logpkg.Warn("no admin user — visit /login to run initialization wizard",
			"hint", "GET /api/auth/init-status, then POST /api/auth/init",
		)
	} else {
		logpkg.Info("admin users present", "count", n)
	}

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
		GetCacheEntries:     makeListCacheEntriesAdapter(db, fs),
		DeleteCacheEntry:    makeDeleteCacheEntryAdapter(db, fs),
		ListCleanupTasks:    makeListCleanupTasksAdapter(db),
		RunCleanupTask:      makeRunCleanupTaskAdapter(db, fs),
		GetUpstreamHealth:   makeGetUpstreamHealthAdapter(upstreamHealth),
		AuthDB:              db, // *storage.DB 满足 api.AuthDB 接口
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

// makeListCleanupTasksAdapter 包装 storage.ListCleanupTasks → api。
func makeListCleanupTasksAdapter(db *storage.DB) func(ctx context.Context) ([]api.CleanupTask, error) {
	return func(ctx context.Context) ([]api.CleanupTask, error) {
		rows, err := db.ListCleanupTasks(ctx)
		if err != nil {
			return nil, err
		}
		out := make([]api.CleanupTask, 0, len(rows))
		for _, r := range rows {
			out = append(out, api.CleanupTask{
				ID:               r.ID,
				Name:             r.Name,
				Strategy:         r.Strategy,
				ThresholdSeconds: r.ThresholdSeconds,
				ThresholdBytes:   r.ThresholdBytes,
				Enabled:          r.Enabled,
				CronIntervalSec:  r.CronIntervalSec,
				LastRunAt:        r.LastRunAt,
				LastStatus:       r.LastStatus,
				LastFreedBytes:   r.LastFreedBytes,
				LastFreedCount:   r.LastFreedCount,
				CreatedAt:        r.CreatedAt,
			})
		}
		return out, nil
	}
}

// makeRunCleanupTaskAdapter 包装 runCleanup → api。
func makeRunCleanupTaskAdapter(db *storage.DB, fs *cache.FileStore) func(ctx context.Context, id int64) (api.CleanupReport, error) {
	return func(ctx context.Context, id int64) (api.CleanupReport, error) {
		rep, err := runCleanup(ctx, db, fs, id)
		if err != nil {
			return api.CleanupReport{}, err
		}
		// 更新 last_run
		_ = db.UpdateCleanupTaskLastRun(ctx, id, "ok", rep.FreedBytes, rep.FreedCount)
		return api.CleanupReport{
			TaskID:      rep.TaskID,
			Strategy:    rep.Strategy,
			FreedCount:  rep.FreedCount,
			FreedBytes:  rep.FreedBytes,
			BeforeCount: rep.BeforeCount,
			BeforeBytes: rep.BeforeBytes,
			AfterCount:  rep.AfterCount,
			AfterBytes:  rep.AfterBytes,
			DurationMs:  rep.DurationMs,
		}, nil
	}
}

// makeGetUpstreamHealthAdapter 包装 *upstreamHealth → api.UpstreamHealth。
func makeGetUpstreamHealthAdapter(h *upstreamHealth) func() api.UpstreamHealth {
	return func() api.UpstreamHealth {
		s := h.Snapshot()
		return api.UpstreamHealth{
			URL:         s.URL,
			Reachable:   s.Reachable,
			LatencyMs:   s.LatencyMs,
			Error:       s.Error,
			LastChecked: s.LastChecked,
		}
	}
}

// metaWriterAdapter 把 proxy.MetaWriter 接口适配到 storage.DB。
type metaWriterAdapter struct {
	db *storage.DB
}

func (m *metaWriterAdapter) UpsertCacheEntry(ctx context.Context, e storage.CacheEntry) (int64, error) {
	return m.db.UpsertCacheEntry(ctx, e)
}

func (m *metaWriterAdapter) TouchCacheEntry(ctx context.Context, registry, repo, digest string) error {
	return m.db.TouchCacheEntry(ctx, registry, repo, digest)
}

// runCleanupScheduler 按 cron 周期跑 cleanup_tasks。
//
// 行为：每 30s 扫描 enabled 任务，看 last_run_at + cron_interval_sec 是否到期；
// 到期则同步跑（fast 1min 内完成，blocking 也不会太久）。
func runCleanupScheduler(ctx context.Context, db *storage.DB, fs *cache.FileStore, cacheTotalGB int) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	// 启动时把 capacity 任务的 threshold_bytes 同步成当前配置（如果用户改了 config 不想重启后跑老值）
	if cacheTotalGB > 0 {
		thresholdBytes := int64(cacheTotalGB) * 1024 * 1024 * 1024
		_, _ = db.SQLDB.ExecContext(ctx, `
			UPDATE cleanup_tasks SET threshold_bytes = ?
			WHERE task_name = 'capacity-cap' AND strategy = 'capacity'
		`, thresholdBytes)
	}

	run := func(taskID int64) {
		// 5s 超时（清理不应阻塞其它 goroutine 太久）
		runCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
		defer cancel()
		report, err := runCleanup(runCtx, db, fs, taskID)
		status := "ok"
		if err != nil {
			status = "error: " + err.Error()
		}
		if uerr := db.UpdateCleanupTaskLastRun(runCtx, taskID, status, report.FreedBytes, report.FreedCount); uerr != nil {
			logpkg.Warn("update cleanup task last_run", "err", uerr.Error(), "task_id", taskID)
		}
		logpkg.Info("cleanup task run",
			"task_id", taskID,
			"strategy", report.Strategy,
			"freed_count", report.FreedCount,
			"freed_bytes", report.FreedBytes,
			"duration_ms", report.DurationMs,
			"status", status,
		)
	}

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		tasks, err := db.ListCleanupTasks(ctx)
		if err != nil {
			logpkg.Warn("list cleanup tasks", "err", err.Error())
			continue
		}
		now := time.Now().Unix()
		for _, t := range tasks {
			if !t.Enabled {
				continue
			}
			interval := int64(t.CronIntervalSec)
			if interval <= 0 {
				interval = 3600
			}
			if now-t.LastRunAt < interval {
				continue
			}
			run(t.ID)
		}
	}
}

// runCleanup 实际跑一个任务（含删文件 + 删 DB 行）。
func runCleanup(ctx context.Context, db *storage.DB, fs *cache.FileStore, taskID int64) (storage.CleanupReport, error) {
	t, err := db.GetCleanupTaskByID(ctx, taskID)
	if err != nil {
		return storage.CleanupReport{}, err
	}
	var report storage.CleanupReport
	switch t.Strategy {
	case "lru":
		report, err = db.RunLRU(ctx, taskID, t.ThresholdSeconds, 200)
	case "capacity":
		report, err = db.RunCapacity(ctx, taskID, t.ThresholdBytes, 200)
	default:
		return storage.CleanupReport{}, fmt.Errorf("unknown strategy: %s", t.Strategy)
	}
	if err != nil {
		return report, err
	}
	// 文件删除走与 DELETE 相同的逻辑：列每个被删的 entry 删 blob 文件
	// （实现简化：从 cache_entries 中找出 last_access_at 旧于 cutoff 的，调 fs.Delete）
	if report.FreedCount > 0 {
		_ = deleteFilesForReport(ctx, db, fs, t, report)
	}
	return report, nil
}

// deleteFilesForReport 清理 run 删掉的行对应的 cache blob 文件。
// 实际策略：跑一次相同 LRU/capacity 选条目列表，逐个调 fs.Delete。
// 注意：可能跟 run 已经删的 row 有重复（race），但 fs.Delete 幂等，不影响。
func deleteFilesForReport(ctx context.Context, db *storage.DB, fs *cache.FileStore, t *storage.CleanupTask, _ storage.CleanupReport) error {
	switch t.Strategy {
	case "lru":
		cutoff := time.Now().Unix() - int64(t.ThresholdSeconds)
		rows, err := db.SQLDB.QueryContext(ctx, `SELECT registry, repository, digest FROM cache_entries WHERE last_access_at < ? AND digest != ''`, cutoff)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var reg, repo, dig string
			if err := rows.Scan(&reg, &repo, &dig); err != nil {
				return err
			}
			if err := fs.Delete(reg, repo, dig); err != nil {
				logpkg.Warn("cleanup: delete file", "err", err.Error(), "digest", dig)
			}
		}
	case "capacity":
		// 跑后再次检查，超过阈值就再删最旧的（直到 ≤ 阈值）
		for {
			var total int64
			if err := db.SQLDB.QueryRowContext(ctx, `SELECT COALESCE(SUM(size_bytes), 0) FROM cache_entries`).Scan(&total); err != nil {
				return err
			}
			if total <= t.ThresholdBytes {
				break
			}
			rows, err := db.SQLDB.QueryContext(ctx, `SELECT registry, repository, digest, size_bytes FROM cache_entries WHERE digest != '' ORDER BY last_access_at ASC LIMIT 50`)
			if err != nil {
				return err
			}
			var batch []struct {
				reg, repo, dig string
				size           int64
			}
			for rows.Next() {
				var b struct {
					reg, repo, dig string
					size           int64
				}
				if err := rows.Scan(&b.reg, &b.repo, &b.dig, &b.size); err != nil {
					rows.Close()
					return err
				}
				batch = append(batch, b)
			}
			rows.Close()
			if len(batch) == 0 {
				break
			}
			for _, b := range batch {
				_, _ = db.SQLDB.ExecContext(ctx, `DELETE FROM cache_entries WHERE registry=? AND repository=? AND digest=?`, b.reg, b.repo, b.dig)
				if err := fs.Delete(b.reg, b.repo, b.dig); err != nil {
					logpkg.Warn("cleanup: delete file", "err", err.Error(), "digest", b.dig)
				}
			}
		}
	}
	return nil
}

// upstreamHealth 缓存上游连通性检测结果（goroutine 安全）。
type upstreamHealth struct {
	mu     sync.RWMutex
	latest UpstreamHealthSnapshot
}

// UpstreamHealthSnapshot 一次检测的快照。
type UpstreamHealthSnapshot struct {
	URL         string `json:"url"`
	Reachable   bool   `json:"reachable"`
	LatencyMs   int64  `json:"latencyMs"`
	Error       string `json:"error,omitempty"`
	LastChecked int64  `json:"lastChecked"`
}

// runSessionCleanup 定期清过期 session（每小时一次）。
func runSessionCleanup(ctx context.Context, db *storage.DB) {
	ticker := time.NewTicker(1 * time.Hour)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			n, err := db.PurgeExpiredSessions(ctx)
			if err != nil {
				logpkg.Warn("purge expired sessions", "err", err.Error())
				continue
			}
			if n > 0 {
				logpkg.Info("purged expired sessions", "count", n)
			}
		}
	}
}

// startUpstreamHealthChecker 启 goroutine 每 60s 探一次上游 registry。
func startUpstreamHealthChecker(ctx context.Context, upstreamURL string) *upstreamHealth {
	h := &upstreamHealth{latest: UpstreamHealthSnapshot{URL: upstreamURL, Reachable: true}}
	hc := &http.Client{Timeout: 5 * time.Second}
	check := func() {
		start := time.Now()
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, upstreamURL+"/v2/", nil)
		req.Header.Set("User-Agent", "cncachehub-healthcheck")
		resp, err := hc.Do(req)
		latency := time.Since(start).Milliseconds()
		snap := UpstreamHealthSnapshot{
			URL:         upstreamURL,
			LatencyMs:   latency,
			LastChecked: time.Now().Unix(),
		}
		if err != nil {
			snap.Reachable = false
			snap.Error = err.Error()
		} else {
			_ = resp.Body.Close()
			// 401 也算 reachable（说明上游响应了，是 network 通）
			snap.Reachable = resp.StatusCode > 0
			if resp.StatusCode >= 500 {
				snap.Reachable = false
				snap.Error = fmt.Sprintf("HTTP %d", resp.StatusCode)
			}
		}
		h.mu.Lock()
		h.latest = snap
		h.mu.Unlock()
	}
	check() // 启动时立即跑一次
	go func() {
		ticker := time.NewTicker(60 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				check()
			}
		}
	}()
	return h
}

// Snapshot 返回当前快照。
func (h *upstreamHealth) Snapshot() UpstreamHealthSnapshot {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.latest
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
			CacheHits:       s.CacheHits,
			BypassedCount:   s.BypassedCount,
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

// makeListCacheEntriesAdapter 包装 storage.ListCacheEntries → api 列表。
func makeListCacheEntriesAdapter(db *storage.DB, fs cache.Store) func(ctx context.Context, page, pageSize int, query string) ([]api.CacheEntry, int, error) {
	return func(ctx context.Context, page, pageSize int, query string) ([]api.CacheEntry, int, error) {
		rows, total, err := db.ListCacheEntries(ctx, page, pageSize, query)
		if err != nil {
			return nil, 0, err
		}
		out := make([]api.CacheEntry, 0, len(rows))
		for _, r := range rows {
			out = append(out, api.CacheEntry{
				ID:           r.ID,
				Registry:     r.Registry,
				Repository:   r.Repository,
				Digest:       r.Digest,
				MediaType:    r.MediaType,
				SizeBytes:    r.SizeBytes,
				StoragePath:  r.StoragePath,
				HitCount:     r.HitCount,
				LastAccessAt: r.LastAccessAt,
				CreatedAt:    r.CreatedAt,
				Bypassed:     r.Bypassed,
				BypassReason: r.BypassReason,
			})
		}
		return out, total, nil
	}
}

// makeDeleteCacheEntryAdapter 删 DB 行 + 调 cache.Store.Delete 删文件。
func makeDeleteCacheEntryAdapter(db *storage.DB, fs cache.Store) func(ctx context.Context, id int64) error {
	return func(ctx context.Context, id int64) error {
		// 先取 entry 拿到 storage_path 三元组，再删文件 + 删行
		e, err := db.GetCacheEntryByID(ctx, id)
		if err != nil {
			return err
		}
		if err := fs.Delete(e.Registry, e.Repository, e.Digest); err != nil {
			logpkg.Warn("delete cache blob file failed", "err", err.Error(), "id", id)
			// 文件不存在不阻断（DB 行才是 source of truth）
		}
		return db.DeleteCacheEntry(ctx, id)
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
