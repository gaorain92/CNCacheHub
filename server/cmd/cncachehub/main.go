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
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/cncachehub/server/internal/access"
	"github.com/cncachehub/server/internal/api"
	"github.com/cncachehub/server/internal/cache"
	"github.com/cncachehub/server/internal/clientip"
	"github.com/cncachehub/server/internal/config"
	"github.com/cncachehub/server/internal/crypto"
	dnsserver "github.com/cncachehub/server/internal/dns"
	"github.com/cncachehub/server/internal/diagnostics"
	logpkg "github.com/cncachehub/server/internal/log"
	"github.com/cncachehub/server/internal/metrics"
	"github.com/cncachehub/server/internal/preheat"
	"github.com/cncachehub/server/internal/proxy"
	"github.com/cncachehub/server/internal/ratelimit"
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

	// 3b. 同步 env -> DB（仅首次；后续 UI 改值不会被重启覆盖）。
	if err := syncEnvToSettings(rootCtx, db, &cfg); err != nil {
		return fmt.Errorf("sync env to settings: %w", err)
	}

	// 3b2. 加载 / 生成 master key（§9.7.3 上游凭据加密）。
	// 优先用 CNCH_MASTER_KEY env 64 字符 hex；否则用 data_dir/.master_key (auto-generate 0600)。
	masterKey, err := crypto.LoadOrCreateMasterKey(cfg.DataDir, os.Getenv("CNCH_MASTER_KEY"))
	if err != nil {
		return fmt.Errorf("load master key: %w", err)
	}
	cipher := &masterKeyCipher{key: masterKey}
	logpkg.Info("credential cipher ready", "key_source", cipherKeySource(cfg.DataDir))

	// 3c. 初始化 access control Resolver（PRD §9.7.2 P2#4）。
	// 从 DB 读最新 4 个 key；admin PUT 改值后通过 set() 重新读。
	accessGet, accessSet := access.MutableResolver(loadAccessConfig(rootCtx, db))
	accessReload := func() {
		accessSet(loadAccessConfig(rootCtx, db))
	}
	logpkg.Info("access control ready",
		"enabled", accessGet().Enabled,
		"token_set", accessGet().Token != "",
		"ip_whitelist_count", len(accessGet().IPWhitelist),
		"loopback_bypass", accessGet().LoopbackBypass,
	)

	// 3c.b 初始化 trusted proxy CIDR（clientip 包）。
	// 配置来源：CNCH_TRUSTED_PROXIES（逗号分隔 CIDR）。
	// 影响：X-Forwarded-For / X-Real-IP header 是否被信任（不信任的来源会忽略，
	// 防止直连 :8082 的攻击者伪造 IP 绕过 rate limit / access control）。
	if len(cfg.TrustedProxies) > 0 {
		clientip.SetTrustedProxies(cfg.TrustedProxies)
		logpkg.Info("trusted proxies configured", "cidrs", cfg.TrustedProxies, "source", "CNCH_TRUSTED_PROXIES")
	} else {
		logpkg.Info("trusted proxies using defaults", "cidrs", "loopback+RFC1918", "override_via", "CNCH_TRUSTED_PROXIES env var")
	}

	// 3d. 公开 Base URL 解析器（client config 生成器用）。
	// 启动时从 DB 读一次；admin 通过 PATCH /api/settings 改值后 reload。
	// 用一个 closure 让 api 每次取最新值（Options 里的 func 字段）。
	var publicBaseURLVal string
	publicBaseURLVal = loadPublicBaseURL(rootCtx, db)
	publicBaseURLReload := func() {
		publicBaseURLVal = loadPublicBaseURL(rootCtx, db)
	}
	publicBaseURLGet := func() string { return publicBaseURLVal }
	if publicBaseURLVal != "" {
		logpkg.Info("public base url", "url", publicBaseURLVal)
	}

	// 4. 初始化 cache + 上游 + proxy。
	// 4a. 优先从 DB 读 settings（UI 改值后重启会保留），fallback 到 cfg。
	maxMB := cfg.MaxObjectSizeMB
	reserveGB := cfg.ReserveSpaceGB
	if s, err := db.GetSetting(rootCtx, storage.SettingMaxObjectSizeMB); err == nil && s.Value != "" {
		if n, perr := strconv.Atoi(s.Value); perr == nil && n > 0 {
			maxMB = n
		}
	}
	if s, err := db.GetSetting(rootCtx, storage.SettingReserveSpaceGB); err == nil && s.Value != "" {
		if n, perr := strconv.Atoi(s.Value); perr == nil && n > 0 {
			reserveGB = n
		}
	}
	fs, err := cache.NewFileStore(cfg.CacheDir, cache.Policy{
		MaxObjectSize: int64(maxMB) * 1024 * 1024,
		ReserveSpace:  int64(reserveGB) * 1024 * 1024 * 1024,
	})
	if err != nil {
		return fmt.Errorf("init cache: %w", err)
	}
	logpkg.Info("cache ready",
		"root", fs.RootDir(),
		"max_object_size_mb", maxMB,
		"reserve_space_gb", reserveGB,
	)

	up, err := proxy.NewUpstreamPool(buildUpstreamPoolEntries(db, &cfg, version))
	if err != nil {
		return fmt.Errorf("init upstream pool: %w", err)
	}
	logpkg.Info("upstream pool ready", "names", up.ListNames())

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

	// 5d2. 启 log retention cleanup goroutine（每 6 小时清过期访问日志）。
	go runLogRetentionCleanup(rootCtx, db)

	// 5e. 启 SteamCMD DNS 启动器（PRD §9.3）。从 DB 读配置；cfg 默认关。
	dnsSrv := dnsserver.NewServer(dnsserver.Config{
		Enabled:     false,
		ListenAddr:  "0.0.0.0:5353",
		Upstream:    "1.1.1.1:53",
		AnswerIP:    "127.0.0.1",
		DomainRules: []string{"*.steamcontent.com", "*.steamstatic.com", "client-download.steampowered.com"},
	}, logpkg.L())

	// 5f. 预热任务执行器（PRD §9.2.3 / §9.5.5）。CNCHBaseURL 走 cfg.HTTPAddr 内部地址。
	preheatRunner := preheat.NewRunner(db, "http://"+cfg.HTTPAddr, logpkg.L())
	if dnsCfg, err := db.GetDNSConfig(rootCtx); err == nil {
		// 启动时 Reload（按 DB 配置决定 enabled / 端口 / 上游 / 答案 IP / 规则）
		if rerr := dnsSrv.Reload(rootCtx, dnsserver.Config{
			Enabled:     dnsCfg.Enabled,
			ListenAddr:  dnsCfg.ListenAddr,
			Upstream:    dnsCfg.Upstream,
			AnswerIP:    dnsCfg.AnswerIP,
			DomainRules: dnsCfg.DomainRules,
			UpdatedAt:   time.Unix(dnsCfg.UpdatedAt, 0),
		}); rerr != nil {
			logpkg.Warn("dns server initial reload failed", "err", rerr)
		}
	}

	// 5f. 检查是否首次启动（无 admin），提醒用户初始化。
	if n, _ := db.CountUsers(rootCtx); n == 0 {
		logpkg.Warn("no admin user — visit /login to run initialization wizard",
			"hint", "GET /api/auth/init-status, then POST /api/auth/init",
		)
	} else {
		logpkg.Info("admin users present", "count", n)
	}

	// 5g. Rate limiters（PRD §15.3 安全）。
	// login: 每 IP 突发 5 次，之后每 10 秒放 1 个（防暴力破解）
	loginLimiter := ratelimit.NewLimiter(5, 0.1, 10*time.Minute)
	// 通用 API: 每 IP 突发 30 次，之后每秒放 5 个（防 DoS）
	apiLimiter := ratelimit.NewLimiter(30, 5, 10*time.Minute)
	logpkg.Info("rate limiters ready",
		"login_capacity", 5, "login_refill_per_sec", 0.1,
		"api_capacity", 30, "api_refill_per_sec", 5,
	)

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
		ResourceHandler:     newResourceHandlerWithHFToken(db, fs, int64(maxMB)*1024*1024, logpkg.L()),
		HFMirrorHandler:     proxy.NewHFMirrorHandler(newResourceHandlerWithHFToken(db, fs, int64(maxMB)*1024*1024, logpkg.L()), hfTokenGetter(db), logpkg.L()),
		GetHuggingFaceToken: hfTokenGetter(db),
		FetchHuggingFaceTree: api.RealFetchHuggingFaceTree(hfTokenGetter(db)),
		AccessLogWriter:     &accessLogBridge{db: db},
		GetUpstreams:        makeUpstreamsAdapter(db),
		GetDashboardSummary: makeDashboardAdapter(db),
		GetAccessLogs:       makeListAccessLogsAdapter(db),
		PurgeAccessLogs:     makePurgeAccessLogsAdapter(db),
		CountAccessLogs:     makeCountAccessLogsAdapter(db),
		GetCacheEntries:     makeListCacheEntriesAdapter(db, fs),
		DeleteCacheEntry:    makeDeleteCacheEntryAdapter(db, fs),
		ListCleanupTasks:    makeListCleanupTasksAdapter(db),
		RunCleanupTask:      makeRunCleanupTaskAdapter(db, fs),
		GetUpstreamHealth:   makeGetUpstreamHealthAdapter(upstreamHealth),
		GetSettings:         makeGetSettingsAdapter(db),
		UpdateSettings:      makeUpdateSettingsAdapter(db, fs, publicBaseURLReload),
		DryRunCleanup:       makeDryRunCleanupAdapter(db),
		ListRegistries:         makeListRegistriesAdapter(db),
		SetRegistryEnabled:     makeSetRegistryEnabledAdapter(db),
		SetRegistryCredentials: db.SetUpstreamCredentials,
		CredentialCipher:       cipher,
		AuthDB:              db, // *storage.DB 满足 api.AuthDB 接口
		// SteamCMD DNS 启动器（PRD §9.3）— 构造 server 实例
		GetDNSConfig:       makeGetDNSConfigAdapter(db),
		UpdateDNSConfig:    makeUpdateDNSConfigAdapter(db),
		DNSServer:          dnsSrv,
		SessionUserRole:    makeSessionUserRoleAdapter(db),
		// SteamCMD AppID 管理（PRD §9.3.3）
		ListSteamAppIDs:     db.ListSteamAppIDs,
		GetSteamAppID:       db.GetSteamAppID,
		CreateSteamAppID:    db.CreateSteamAppID,
		UpdateSteamAppID:    db.UpdateSteamAppID,
		DeleteSteamAppID:    db.DeleteSteamAppID,
		RecordPreheatResult: db.RecordPreheatResult,
		// 通用预热任务（PRD §9.2.3 / §9.5.5）
		ListPreheatTasks:  db.ListPreheatTasks,
		GetPreheatTask:    db.GetPreheatTask,
		CreatePreheatTask: db.CreatePreheatTask,
		DeletePreheatTask: db.DeletePreheatTask,
		ListPreheatItems:  db.ListPreheatItems,
		RunPreheatTask:    preheatRunner.RunTask,
		CancelPreheatTask: preheatRunner.CancelTask,
		// 诊断中心（PRD §9.7）
		RunDiagnostics: makeRunDiagnostics(db, dnsSrv, &cfg),
		// 资源加速中心（PRD §9.4）
		ListResourceRules:        db.ListResourceRules,
		CreateResourceRule:       db.CreateResourceRule,
		UpdateResourceRule:       db.UpdateResourceRule,
		DeleteResourceRule:       db.DeleteResourceRule,
		ListResourceCache:        db.ListResourceCache,
		DeleteResourceCacheEntry: db.DeleteResourceCacheEntry,
		// Prometheus metrics（P2#2）
		MetricsDB:         db,
		MetricsDNSServer:  dnsSrv,
		MetricsUpstreams:  makeMetricsUpstreams(upstreamHealth),
		MetricsVersion:    version,
		MetricsCommit:     commit,
		MetricsStartTime:  cfg.StartTime,
		// 诊断包导出（P2#3）— BundleSource 注入
		BundleSource: diagnostics.BundleSource{
			DB:           db,
			Version:      version,
			Commit:       commit,
			StartTime:    cfg.StartTime,
			HTTPAddr:     cfg.HTTPAddr,
			CacheDir:     cfg.CacheDir,
			DataDir:      cfg.DataDir,
			UpstreamURL:  cfg.UpstreamRegistry,
			MaxObjectMB:  maxMB,
			ReserveGB:    reserveGB,
			CacheTotalGB: cfg.CacheTotalGB,
			// LogPath 留空（server 走 slog stderr，没落文件；未来加文件日志时填上）
		},
		// 代理访问控制（P2#4 / PRD §9.7.2）— Resolver + 热重载回调
		AccessControlResolve: accessGet,
		AccessControlReload:  accessReload,
		// Rate limiters（PRD §15.3）
		LoginRateLimiter: loginLimiter,
		APIRateLimiter:   apiLimiter,
		// 公开 Base URL（client config 生成器用，admin PATCH settings 后会 reload）
		PublicBaseURL: publicBaseURLGet,
		// client config 生成器需 GetSettings + ListRegistries；Options 已含两者
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
	// 关 DNS server
	if err := dnsSrv.Stop(); err != nil {
		logpkg.Warn("dns server stop", "err", err)
	}
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
		CreatedAt:    time.Now().Unix(),
		Method:       rec.Method,
		Path:         rec.Path,
		Status:       rec.Status,
		DurationMs:   rec.DurationMs,
		Cached:       rec.Cached,
		Bypassed:     rec.Bypassed != cache.BypassNone,
		BypassReason: string(rec.Bypassed), // '' / 'size_limit' / 'disk_low'
		ClientIP:     rec.ClientIP,
		Bytes:        rec.Bytes,
		Error:        rec.Error,
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

// makeGetSettingsAdapter 读 system_settings → api.SystemSettings。
func makeGetSettingsAdapter(db *storage.DB) func(ctx context.Context) (api.SystemSettings, error) {
	return func(ctx context.Context) (api.SystemSettings, error) {
		settings, err := db.ListSettings(ctx)
		if err != nil {
			return api.SystemSettings{}, err
		}
		out := api.SystemSettings{}
		var latest int64
		for _, s := range settings {
			switch s.Key {
			case storage.SettingSmallVPSOpt:
				out.SmallVPSOpt = s.Value == "true"
			case storage.SettingReserveSpaceGB:
				out.ReserveSpaceGB = atoiSafe(s.Value, 5)
			case storage.SettingMaxObjectSizeMB:
				out.MaxObjectSizeMB = atoiSafe(s.Value, 1024)
			case storage.SettingCacheTotalGB:
				out.CacheTotalGB = atoiSafe(s.Value, 20)
			case storage.SettingCleanupTriggerPct:
				out.CleanupTriggerPct = atoiSafe(s.Value, 80)
			case storage.SettingCleanupTargetPct:
				out.CleanupTargetPct = atoiSafe(s.Value, 60)
			case storage.SettingPublicBaseURL:
				out.PublicBaseURL = s.Value
			case storage.SettingLogRetentionDays:
				out.LogRetentionDays = atoiSafe(s.Value, 30)
			case storage.SettingHuggingFaceToken:
				out.HuggingFaceTokenSet = s.Value != ""
			}
			if s.UpdatedAt > latest {
				latest = s.UpdatedAt
			}
		}
		out.UpdatedAt = latest
		return out, nil
	}
}

// makeUpdateSettingsAdapter PATCH 部分字段。
//
// onUpdate 可选：写完 DB 后调用（用来热重载 public base URL 等）。
func makeUpdateSettingsAdapter(db *storage.DB, fs *cache.FileStore, onUpdate func()) func(ctx context.Context, patch api.SettingsPatch, userID int64) (api.SystemSettings, error) {
	return func(ctx context.Context, patch api.SettingsPatch, userID int64) (api.SystemSettings, error) {
		kvs := map[string]string{}
		if patch.SmallVPSOpt != nil {
			if *patch.SmallVPSOpt {
				kvs[storage.SettingSmallVPSOpt] = "true"
			} else {
				kvs[storage.SettingSmallVPSOpt] = "false"
			}
		}
		if patch.ReserveSpaceGB != nil {
			kvs[storage.SettingReserveSpaceGB] = itoa(*patch.ReserveSpaceGB)
		}
		if patch.MaxObjectSizeMB != nil {
			kvs[storage.SettingMaxObjectSizeMB] = itoa(*patch.MaxObjectSizeMB)
		}
		if patch.CacheTotalGB != nil {
			kvs[storage.SettingCacheTotalGB] = itoa(*patch.CacheTotalGB)
		}
		if patch.CleanupTriggerPct != nil {
			kvs[storage.SettingCleanupTriggerPct] = itoa(*patch.CleanupTriggerPct)
		}
		if patch.CleanupTargetPct != nil {
			kvs[storage.SettingCleanupTargetPct] = itoa(*patch.CleanupTargetPct)
		}
		if patch.PublicBaseURL != nil {
			kvs[storage.SettingPublicBaseURL] = strings.TrimRight(*patch.PublicBaseURL, "/")
		}
		if patch.LogRetentionDays != nil {
			kvs[storage.SettingLogRetentionDays] = itoa(*patch.LogRetentionDays)
		}
		if patch.HuggingFaceToken != nil {
			// 空串视为清空
			if strings.TrimSpace(*patch.HuggingFaceToken) == "" {
				kvs[storage.SettingHuggingFaceToken] = ""
			} else {
				kvs[storage.SettingHuggingFaceToken] = strings.TrimSpace(*patch.HuggingFaceToken)
			}
		}
		if patch.ClearHuggingFaceToken {
			kvs[storage.SettingHuggingFaceToken] = ""
		}
		if err := db.SetMany(ctx, kvs, userID); err != nil {
			return api.SystemSettings{}, err
		}
		if patch.CacheTotalGB != nil {
			thresholdBytes := int64(*patch.CacheTotalGB) * 1024 * 1024 * 1024
			_, _ = db.SQLDB.ExecContext(ctx, `
				UPDATE cleanup_tasks SET threshold_bytes = ?
				WHERE task_name = 'capacity-cap' AND strategy = 'capacity'
			`, thresholdBytes)
		}
		// 热重载 cache policy（PRD §9.1.4 要求"用户可随时调整"）。
		if fs != nil {
			s, _ := makeGetSettingsAdapter(db)(ctx)
			fs.SetPolicy(cache.Policy{
				MaxObjectSize: int64(s.MaxObjectSizeMB) * 1024 * 1024,
				ReserveSpace:  int64(s.ReserveSpaceGB) * 1024 * 1024 * 1024,
			})
			logpkg.Info("cache policy reloaded",
				"max_object_size_mb", s.MaxObjectSizeMB,
				"reserve_space_gb", s.ReserveSpaceGB,
				"small_vps_opt", s.SmallVPSOpt,
			)
		}
		// 其它热重载钩子（public base URL 等）
		if onUpdate != nil {
			onUpdate()
		}
		if userID > 0 {
			_ = db.WriteAudit(ctx, storage.AuditLog{
				UserID:    userID,
				Action:    "update_settings",
				Status:    "ok",
				Details:   summarizePatch(patch),
				CreatedAt: time.Now().Unix(),
			})
		}
		return makeGetSettingsAdapter(db)(ctx)
	}
}

// buildUpstreamPoolEntries 从 DB 读 enabled upstreams + 构造 UpstreamPoolEntry 列表。
//
// dockerhub 的 upstream_url fallback 到 cfg.UpstreamRegistry（env 配的）。
func buildUpstreamPoolEntries(db *storage.DB, cfg *config.Config, version string) []proxy.UpstreamPoolEntry {
	ctx := context.Background()
	ups, err := db.ListEnabledUpstreams(ctx)
	if err != nil || len(ups) == 0 {
		// fallback：用 cfg + dockerhub 默认 seed
		logpkg.Warn("no enabled upstreams in DB, fallback to cfg.UpstreamRegistry as dockerhub",
			"err", err)
		return []proxy.UpstreamPoolEntry{{
			Name:    "dockerhub",
			BaseURL: cfg.UpstreamRegistry,
			Timeout: cfg.UpstreamTimeout,
			UA:      fmt.Sprintf("cncachehub/%s", version),
		}}
	}
	ua := fmt.Sprintf("cncachehub/%s", version)
	out := make([]proxy.UpstreamPoolEntry, 0, len(ups))
	for _, u := range ups {
		// dockerhub 没在 DB 配 upstream_url 时，env 兜底
		base := u.UpstreamURL
		if base == "" && u.Name == "dockerhub" {
			base = cfg.UpstreamRegistry
		}
		if base == "" {
			logpkg.Warn("upstream has no url, skip", "name", u.Name)
			continue
		}
		out = append(out, proxy.UpstreamPoolEntry{
			Name:    u.Name,
			BaseURL: base,
			Timeout: cfg.UpstreamTimeout,
			UA:      ua,
		})
	}
	return out
}

// makeDryRunCleanupAdapter 包装 dryRunCleanup → api。
func makeDryRunCleanupAdapter(db *storage.DB) func(ctx context.Context, id int64) (api.CleanupReport, error) {
	return func(ctx context.Context, id int64) (api.CleanupReport, error) {
		rep, err := dryRunCleanup(ctx, db, id)
		if err != nil {
			return api.CleanupReport{}, err
		}
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

// syncEnvToSettings 把 cfg 里的 env 值同步进 DB（启动时一次）。
//
// 只覆盖表里不存在的 key（INSERT OR IGNORE）；UI 后续改值不会被重启覆盖。
func syncEnvToSettings(ctx context.Context, db *storage.DB, cfg *config.Config) error {
	kvs := map[string]string{
		storage.SettingSmallVPSOpt:     boolToStr(cfg.SmallVPSOpt),
		storage.SettingReserveSpaceGB:  itoa(cfg.ReserveSpaceGB),
		storage.SettingMaxObjectSizeMB: itoa(cfg.MaxObjectSizeMB),
		storage.SettingCacheTotalGB:    itoa(cfg.CacheTotalGB),
	}
	now := time.Now().Unix()
	for k, v := range kvs {
		_, err := db.SQLDB.ExecContext(ctx, `
			INSERT OR IGNORE INTO system_settings (key, value, updated_at, updated_by)
			VALUES (?, ?, ?, 0)
		`, k, v, now)
		if err != nil {
			return err
		}
	}
	return nil
}

// loadAccessConfig 从 DB 读 4 个 access control key，构造 access.Config。
//
// 任何 key 缺失 = 对应默认值。Enabled/LoopbackBypass 默认 false/false 但实际上 UI 默认开 LoopbackBypass。
func loadAccessConfig(ctx context.Context, db *storage.DB) access.Config {
	settings, err := db.GetMany(ctx,
		storage.SettingAccessControlEnabled,
		storage.SettingAccessControlToken,
		storage.SettingAccessControlIPWhitelist,
		storage.SettingAccessControlLoopbackBypass,
	)
	if err != nil {
		logpkg.Warn("load access control config", "err", err.Error())
		return access.Config{LoopbackBypass: true} // fail-open + loopback bypass
	}
	return access.Config{
		Enabled:        settings[storage.SettingAccessControlEnabled] == "true",
		Token:          settings[storage.SettingAccessControlToken],
		IPWhitelist:    access.ParseCIDRList(settings[storage.SettingAccessControlIPWhitelist]),
		LoopbackBypass: settings[storage.SettingAccessControlLoopbackBypass] != "false", // 默认 true
	}
}

// loadPublicBaseURL 从 DB 读 SettingPublicBaseURL。
//
// 缺失/出错返空字符串。空字符串时 api 用 r.Host 兜底。
func loadPublicBaseURL(ctx context.Context, db *storage.DB) string {
	s, err := db.GetSetting(ctx, storage.SettingPublicBaseURL)
	if err != nil || s == nil {
		return ""
	}
	return strings.TrimRight(s.Value, "/")
}

// masterKeyCipher 实现 api.Cipher 接口（用 internal/crypto 的 AES-256-GCM）。
type masterKeyCipher struct {
	key []byte
}

func (c *masterKeyCipher) Encrypt(plaintext []byte) ([]byte, error) {
	return crypto.Encrypt(c.key, plaintext)
}

func (c *masterKeyCipher) Decrypt(ciphertext []byte) ([]byte, error) {
	return crypto.Decrypt(c.key, ciphertext)
}

// cipherKeySource 返回 master key 来自哪（用于启动日志）。
func cipherKeySource(dataDir string) string {
	if os.Getenv("CNCH_MASTER_KEY") != "" {
		return "env:CNCH_MASTER_KEY"
	}
	path := filepath.Join(dataDir, crypto.MasterKeyFilename)
	if _, err := os.Stat(path); err == nil {
		return "file:" + path
	}
	return "(generated on first write)"
}

func boolToStr(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

func itoa(n int) string {
	return strconv.Itoa(n)
}

func atoiSafe(s string, fallback int) int {
	if s == "" {
		return fallback
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return fallback
	}
	return n
}

func summarizePatch(p api.SettingsPatch) string {
	parts := []string{}
	if p.SmallVPSOpt != nil {
		parts = append(parts, "small_vps_opt="+boolToStr(*p.SmallVPSOpt))
	}
	if p.ReserveSpaceGB != nil {
		parts = append(parts, "reserve_space_gb="+itoa(*p.ReserveSpaceGB))
	}
	if p.MaxObjectSizeMB != nil {
		parts = append(parts, "max_object_size_mb="+itoa(*p.MaxObjectSizeMB))
	}
	if p.CacheTotalGB != nil {
		parts = append(parts, "cache_total_gb="+itoa(*p.CacheTotalGB))
	}
	if p.CleanupTriggerPct != nil {
		parts = append(parts, "cleanup_trigger_pct="+itoa(*p.CleanupTriggerPct))
	}
	if p.CleanupTargetPct != nil {
		parts = append(parts, "cleanup_target_pct="+itoa(*p.CleanupTargetPct))
	}
	if p.PublicBaseURL != nil {
		parts = append(parts, "public_base_url="+*p.PublicBaseURL)
	}
	if p.LogRetentionDays != nil {
		parts = append(parts, "log_retention_days="+itoa(*p.LogRetentionDays))
	}
	if p.HuggingFaceToken != nil {
		// 审计日志不写明文 token
		parts = append(parts, "huggingface_token=<redacted len="+itoa(len(strings.TrimSpace(*p.HuggingFaceToken)))+">")
	}
	if p.ClearHuggingFaceToken {
		parts = append(parts, "huggingface_token=<cleared>")
	}
	if len(parts) == 0 {
		return "noop"
	}
	out := ""
	for i, q := range parts {
		if i > 0 {
			out += ","
		}
		out += q
	}
	return out
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
		report, err = db.RunLRU(ctx, taskID, t.ThresholdSeconds, 200, false)
	case "capacity":
		report, err = db.RunCapacity(ctx, taskID, t.ThresholdBytes, 200, false)
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

// dryRunCleanup 干跑一次清理任务（不算真删）。
//
// 返回的 report.freed_count / freed_bytes 是预估；after_* = before_* - freed_*。
// 不动 DB 行，不动文件。
func dryRunCleanup(ctx context.Context, db *storage.DB, taskID int64) (storage.CleanupReport, error) {
	t, err := db.GetCleanupTaskByID(ctx, taskID)
	if err != nil {
		return storage.CleanupReport{}, err
	}
	switch t.Strategy {
	case "lru":
		return db.RunLRU(ctx, taskID, t.ThresholdSeconds, 200, true)
	case "capacity":
		return db.RunCapacity(ctx, taskID, t.ThresholdBytes, 200, true)
	default:
		return storage.CleanupReport{}, fmt.Errorf("unknown strategy: %s", t.Strategy)
	}
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

// runLogRetentionCleanup 定期清理过期访问日志（每 6 小时一次）。
// 保留天数从 system_settings 读，0 = 不清理。
func runLogRetentionCleanup(ctx context.Context, db *storage.DB) {
	ticker := time.NewTicker(6 * time.Hour)
	defer ticker.Stop()
	// 启动后 10 分钟跑第一次（不阻塞 startup）
	initial := time.After(10 * time.Minute)
	for {
		select {
		case <-ctx.Done():
			return
		case <-initial:
			initial = nil
		case <-ticker.C:
		}
		daysStr := db.GetString(ctx, storage.SettingLogRetentionDays, "30")
		days := atoiSafe(daysStr, 30)
		if days <= 0 {
			continue
		}
		before := time.Now().AddDate(0, 0, -days).Unix()
		n, err := db.PurgeAccessLogs(ctx, before)
		if err != nil {
			logpkg.Warn("purge access logs", "err", err.Error())
			continue
		}
		if n > 0 {
			logpkg.Info("purged access logs", "count", n, "retention_days", days)
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

// makeListRegistriesAdapter 列出所有 registry（含 disabled）+ 凭据状态标志。
// 直接返回 storage.Registry（已含 Username/HasPassword/HasToken，§9.7.3）。
func makeListRegistriesAdapter(db *storage.DB) func(ctx context.Context) ([]storage.Registry, error) {
	return func(ctx context.Context) ([]storage.Registry, error) {
		return db.ListAllUpstreams(ctx)
	}
}

// makeSetRegistryEnabledAdapter 启停 upstream。
func makeSetRegistryEnabledAdapter(db *storage.DB) func(ctx context.Context, name string, enabled bool) error {
	return func(ctx context.Context, name string, enabled bool) error {
		return db.SetUpstreamEnabled(ctx, name, enabled)
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

// makeListAccessLogsAdapter 返回闭包，把 storage.AccessLogRecord 转为 api.AccessLogRecord（含筛选）。
func makeListAccessLogsAdapter(db *storage.DB) func(ctx context.Context, page, pageSize int, filter api.LogFilter) ([]api.AccessLogRecord, int, error) {
	return func(ctx context.Context, page, pageSize int, f api.LogFilter) ([]api.AccessLogRecord, int, error) {
		sf := storage.LogFilter{
			Status:    f.Status,
			StatusCls: f.StatusCls,
			Method:    f.Method,
			Path:      f.Path,
			Cached:    f.Cached,
			Bypassed:  f.Bypassed,
			ClientIP:  f.ClientIP,
			StartAt:   f.StartAt,
			EndAt:     f.EndAt,
		}
		rows, total, err := db.ListAccessLogs(ctx, page, pageSize, sf)
		if err != nil {
			return nil, 0, err
		}
		out := make([]api.AccessLogRecord, 0, len(rows))
		for _, r := range rows {
			out = append(out, api.AccessLogRecord{
				ID:           r.ID,
				CreatedAt:    r.CreatedAt,
				Method:       r.Method,
				Path:         r.Path,
				Status:       r.Status,
				DurationMs:   r.DurationMs,
				Cached:       r.Cached,
				Bypassed:     r.Bypassed,
				BypassReason: r.BypassReason,
				ClientIP:     r.ClientIP,
				Bytes:        r.Bytes,
				Error:        r.Error,
			})
		}
		return out, total, nil
	}
}

// makePurgeAccessLogsAdapter 返回闭包，转发到 storage.PurgeAccessLogs。
func makePurgeAccessLogsAdapter(db *storage.DB) func(ctx context.Context, before int64) (int64, error) {
	return func(ctx context.Context, before int64) (int64, error) {
		return db.PurgeAccessLogs(ctx, before)
	}
}

// makeCountAccessLogsAdapter 返回闭包，转发到 storage.CountAccessLogs。
func makeCountAccessLogsAdapter(db *storage.DB) func(ctx context.Context) (int, error) {
	return func(ctx context.Context) (int, error) {
		return db.CountAccessLogs(ctx)
	}
}

// makeGetDNSConfigAdapter 返回 DB 的 dns_config 读 closure。
func makeGetDNSConfigAdapter(db *storage.DB) func(ctx context.Context) (storage.DNSConfig, error) {
	return func(ctx context.Context) (storage.DNSConfig, error) {
		return db.GetDNSConfig(ctx)
	}
}

// makeUpdateDNSConfigAdapter 返回 DB 的 dns_config 写 closure。
func makeUpdateDNSConfigAdapter(db *storage.DB) func(ctx context.Context, patch storage.DNSConfigPatch) (storage.DNSConfig, error) {
	return func(ctx context.Context, patch storage.DNSConfigPatch) (storage.DNSConfig, error) {
		return db.UpdateDNSConfig(ctx, patch)
	}
}

// makeSessionUserRoleAdapter 从 cookie 拿 session token → user role。
// 返回 (role, userID, error)；未登录返回 ("", 0, nil)。
func makeSessionUserRoleAdapter(db *storage.DB) func(ctx context.Context, r *http.Request) (string, int64, error) {
	return func(ctx context.Context, r *http.Request) (string, int64, error) {
		c, err := r.Cookie("cnsid")
		if err != nil || c.Value == "" {
			return "", 0, nil
		}
		sess, err := db.GetSession(ctx, c.Value)
		if err != nil {
			return "", 0, nil
		}
		u, err := db.GetUserByID(ctx, sess.UserID)
		if err != nil {
			return "", 0, err
		}
		role := "user"
		if u.IsAdmin {
			role = "admin"
		}
		return role, u.ID, nil
	}
}

// makeRunDiagnostics 构造诊断中心的 closure。
// 包含 DNS server stats + access log 聚合 + docker daemon.json 读取。
func makeRunDiagnostics(db *storage.DB, dnsSrv *dnsserver.Server, cfg *config.Config) func(ctx context.Context) diagnostics.FullReport {
	return func(ctx context.Context) diagnostics.FullReport {
		// 解析 public base URL — 从 cfg.HTTPAddr（127.0.0.1:8082）换成本机非 loopback IP
		// 简单做法：取本机 hostname 解析的第一个非 loopback IPv4。
		publicBase := "http://" + firstNonLoopbackIPv4()
		if cfg.HTTPAddr != "" && !strings.HasPrefix(cfg.HTTPAddr, "127.") && !strings.HasPrefix(cfg.HTTPAddr, "0.0.0.0") {
			publicBase = "http://" + cfg.HTTPAddr
		}
		return diagnostics.RunAll(ctx, diagnostics.RunnerOptions{
			CNCHBaseURL:   "http://" + cfg.HTTPAddr,
			PublicBaseURL: publicBase,
			UpstreamURL:   cfg.UpstreamRegistry,
			DNSServerStats: func() diagnostics.DNSStats {
				cfg2 := dnsSrv.Config()
				stats2 := dnsSrv.Stats()
				return diagnostics.DNSStats{
					Enabled:        cfg2.Enabled,
					ListenAddr:     cfg2.ListenAddr,
					DomainRules:    cfg2.DomainRules,
					LastQueryAt:    stats2.LastQueryAt,
					TotalQueries:   stats2.TotalQueries,
					HitQueries:     stats2.HitQueries,
					MissQueries:    stats2.MissQueries,
					BlockedQueries: stats2.BlockedQueries,
				}
			},
			AccessLogCount: func() (int, int, int, error) {
				d, err := db.DashboardSummary(ctx)
				if err != nil {
					return 0, 0, 0, err
				}
				s5xx, err := db.Count24hStatus(ctx, 500)
				if err != nil {
					return d.RequestCount24h, d.ErrorCount24h, 0, err
				}
				return d.RequestCount24h, d.ErrorCount24h, s5xx, nil
			},
			DaemonConfig: func() (mirrors []string, insecure bool) {
				return readDockerDaemonConfig()
			},
		})
	}
}

// firstNonLoopbackIPv4 拿本机第一个非 127 / IPv6 的 IPv4。
// 简单 net.InterfaceByName + Addrs()。
func firstNonLoopbackIPv4() string {
	ifaces, err := net.Interfaces()
	if err != nil {
		return "127.0.0.1"
	}
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, _ := iface.Addrs()
		for _, a := range addrs {
			var ip net.IP
			switch v := a.(type) {
			case *net.IPNet:
				ip = v.IP
			case *net.IPAddr:
				ip = v.IP
			}
			if ip == nil || ip.IsLoopback() {
				continue
			}
			ip4 := ip.To4()
			if ip4 == nil {
				continue
			}
			return ip4.String()
		}
	}
	return "127.0.0.1"
}

// readDockerDaemonConfig 读 /etc/docker/daemon.json 拿 registry-mirrors。
// 错误时返回空 slice（call site 当 warning 处理）。
func readDockerDaemonConfig() ([]string, bool) {
	data, err := os.ReadFile("/etc/docker/daemon.json")
	if err != nil {
		return nil, false
	}
	var dc struct {
		RegistryMirrors   []string `json:"registry-mirrors"`
		InsecureRegistries []string `json:"insecure-registries"`
	}
	if err := json.Unmarshal(data, &dc); err != nil {
		return nil, false
	}
	// insecure = 任何一个 mirror / insecure-registry 是 http://（明文）。
	// 跟具体 IP 无关 — 任何 HTTP endpoint 都不该走生产 daemon。
	//
	// 旧逻辑：检查 r 是否含特定 IP（含本测试机 IP）— 是遗留的"检测是否连
	// 我们的测试机"启发式，对生产用户无意义且会泄露测试机 IP。已删。
	insecure := false
	for _, r := range dc.InsecureRegistries {
		if strings.HasPrefix(strings.ToLower(r), "http://") {
			insecure = true
			break
		}
	}
	if !insecure {
		for _, r := range dc.RegistryMirrors {
			if strings.HasPrefix(strings.ToLower(r), "http://") {
				insecure = true
				break
			}
		}
	}
	return dc.RegistryMirrors, insecure
}

// makeMetricsUpstreams 包装 *upstreamHealth → []metrics.UpstreamStatus。
func makeMetricsUpstreams(h *upstreamHealth) func() []metrics.UpstreamStatus {
	return func() []metrics.UpstreamStatus {
		snap := h.Snapshot()
		// upstreamHealth.Snapshot() 当前是单条 snapshot（一个主 upstream），
		// 但为了 Prometheus 维度化好，每个上游都开一条
		out := []metrics.UpstreamStatus{
			{
				Name:      registryHostname(snap.URL),
				URL:       snap.URL,
				Reachable: snap.Reachable,
			},
		}
		return out
	}
}

// registryHostname 从 URL 提取主机名（去掉 scheme/port）。
func registryHostname(u string) string {
	u = strings.TrimPrefix(u, "https://")
	u = strings.TrimPrefix(u, "http://")
	if i := strings.Index(u, "/"); i >= 0 {
		u = u[:i]
	}
	if i := strings.Index(u, ":"); i >= 0 {
		u = u[:i]
	}
	if u == "" {
		return "unknown"
	}
	return u
}

// newResourceHandlerWithHFToken 构造 ResourceHandler 并注入 HF token getter。
//
// token 从 system_settings.huggingface_token 读（resource handler 自己每次拉最新值），
// 这样 UI 改 token 后下次请求即生效，无需重启。
func newResourceHandlerWithHFToken(db *storage.DB, fs *cache.FileStore, maxObjectSize int64, logger *slog.Logger) *proxy.ResourceHandler {
	h := proxy.NewResourceHandler(db, fs, maxObjectSize, logger)
	h.GetHuggingFaceToken = hfTokenGetter(db)
	return h
}

// hfTokenGetter 返回一个 closure，从 system_settings 读最新 HF token。
// 用于 API 层（huggingface_handlers）+ proxy.ResourceHandler，行为一致。
func hfTokenGetter(db *storage.DB) func() string {
	return func() string {
		setting, err := db.GetSetting(context.Background(), storage.SettingHuggingFaceToken)
		if err != nil || setting == nil {
			return ""
		}
		return setting.Value
	}
}
