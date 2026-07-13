// Command cncachehub 是 CNCacheHub 的入口程序（Phase 0 骨架）。
//
// 启动流程：
//  1. 加载配置（config.Load）—— 校验失败立即退出；
//  2. 初始化日志（slog JSON + 敏感字段脱敏）；
//  3. 打开 SQLite + 跑 migrations；
//  4. 启动 HTTP server（chi 路由）；
//  5. 监听 SIGINT / SIGTERM，收到后 graceful shutdown（30s 超时）。
//
// 不做的事（明确边界）：
//   - 不写业务逻辑（Phase 0 没有 pull-through cache / SteamCMD / 资源加速）；
//   - 不写鉴权 / session / login；
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
	"github.com/cncachehub/server/internal/config"
	logpkg "github.com/cncachehub/server/internal/log"
	"github.com/cncachehub/server/internal/storage"
)

// 编译期可由 -ldflags 覆盖的变量。
var (
	version = "dev"
	commit  = "local"
)

func main() {
	if err := run(); err != nil {
		// 启动期没有 logger（避免循环依赖），直接 stderr。
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
	// 打印生效配置（不打印敏感字段；redactingHandler 会做最后一道防线）。
	logpkg.Info("config loaded",
		"http_addr", cfg.HTTPAddr,
		"data_dir", cfg.DataDir,
		"db_path", cfg.DBPath,
		"log_level", cfg.LogLevel,
		"shutdown_timeout_seconds", int(cfg.ShutdownTimeout.Seconds()),
		"admin_password", cfg.AdminPassword, // 由 redactingHandler 脱敏为 "***"
	)
	// 显式输出一行 "listening on :8080" 在 HTTP 启动后；这里先标记启动开始。
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

	// 4. 构造 HTTP server。
	build := api.BuildInfo{
		Name:    "cncachehub",
		Version: version,
		Go:      runtime.Version(),
		Commit:  commit,
	}
	handler := api.NewRouter(api.Options{
		DB:        db,
		StartTime: cfg.StartTime,
		Build:     build,
	})
	srv := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      0, // 流式下载需要无写超时
		IdleTimeout:       120 * time.Second,
	}

	// 5. 启动 + 信号处理。
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
		// errCh 正常关闭（http.ErrServerClosed），继续走 shutdown。
	}

	// 6. Graceful shutdown。
	shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("http shutdown: %w", err)
	}
	logpkg.Info("http server stopped")
	return nil
}
