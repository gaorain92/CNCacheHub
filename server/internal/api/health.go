package api

import (
	"context"
	"net/http"
	"time"

	chimw "github.com/go-chi/chi/v5/middleware"
)

// rootHealthHandler 是 /healthz 的轻量处理器：用于 liveness probe。
//
// 只回答 "ok"，不访问依赖。K8s liveness 不应因为 DB 抖动而重启 Pod。
func rootHealthHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{
			"status": "ok",
		})
	}
}

// apiHealthHandler 是 /api/healthz 的完整处理器：用于 readiness probe / 控制台总览。
//
// 字段：
//   - status: 固定 "ok"（只要 handler 跑到了）；
//   - uptime: 从 server 启动到现在的人类可读时长；
//   - db:     "ok" / "error: <msg>"；
//   - version: Build.Version。
func apiHealthHandler(opts Options) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()

		dbStatus := "ok"
		if opts.DB != nil {
			pingCtx, pingCancel := context.WithTimeout(ctx, 1*time.Second)
			if err := opts.DB.Ping(pingCtx); err != nil {
				dbStatus = "error: " + err.Error()
			}
			pingCancel()
		} else {
			dbStatus = "not_configured"
		}

		uptime := time.Since(opts.StartTime).Round(time.Second).String()

		// 透出 request_id，方便排障。
		reqID := chimw.GetReqID(r.Context())
		writeJSON(w, http.StatusOK, map[string]any{
			"status":     "ok",
			"uptime":     uptime,
			"db":         dbStatus,
			"version":    opts.Build.Version,
			"request_id": reqID,
		})
	}
}
