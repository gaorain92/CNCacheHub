// Package api: 业务 handler（Phase 1）。
package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
)

// upstreamsHandler GET /api/docker/upstreams
func upstreamsHandler(opts Options) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if opts.GetUpstreams == nil {
			writeError(w, http.StatusServiceUnavailable, "upstreams_unavailable", "upstreams not configured")
			return
		}
		ups, err := opts.GetUpstreams(r.Context())
		if err != nil {
			writeInternalErr(w, r, "upstreams_query_failed", err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"items": ups,
			"total": len(ups),
		})
	}
}

// daemonJSONHandler GET /api/docker/daemon.json
//
// 返回可直接粘贴到 docker daemon 的 JSON 片段（不是文件，client 自己负责落盘）。
// Query: ?upstream=dockerhub（默认）。
func daemonJSONHandler(opts Options) http.HandlerFunc {
	type daemonConfig struct {
		RegistryMirrors     []string `json:"registry-mirrors,omitempty"`
		InsecureRegistries  []string `json:"insecure-registries,omitempty"`
	}
	return func(w http.ResponseWriter, r *http.Request) {
		if opts.GetUpstreams == nil {
			writeError(w, http.StatusServiceUnavailable, "upstreams_unavailable", "upstreams not configured")
			return
		}
		ups, err := opts.GetUpstreams(r.Context())
		if err != nil {
			writeInternalErr(w, r, "upstreams_query_failed", err)
			return
		}
		if len(ups) == 0 {
			writeError(w, http.StatusNotFound, "no_upstream", "no enabled upstream")
			return
		}
		// 选第一个
		up := ups[0]
		// 构造 mirror URL：从 request Host 推断（避免硬编码）
		scheme := "http"
		if r.TLS != nil {
			scheme = "https"
		}
		if fwd := r.Header.Get("X-Forwarded-Proto"); fwd != "" {
			scheme = fwd
		}
		host := r.Host
		mirrorURL := fmt.Sprintf("%s://%s%s", scheme, host, up.MirrorPath)

		cfg := daemonConfig{
			RegistryMirrors:    []string{mirrorURL},
			InsecureRegistries: []string{host},
		}
		b, err := json.MarshalIndent(cfg, "", "  ")
		if err != nil {
			writeInternalErr(w, r, "marshal_failed", err)
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("X-CNCacheHub-Config-For", up.Name)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(b)
		_, _ = w.Write([]byte("\n"))
	}
}

// cacheEntriesHandler GET /api/cache/entries?page=1&pageSize=20&q=nginx
func cacheEntriesHandler(opts Options) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if opts.GetCacheEntries == nil {
			writeError(w, http.StatusServiceUnavailable, "cache_unavailable", "cache not configured")
			return
		}
		page, _ := strconv.Atoi(r.URL.Query().Get("page"))
		pageSize, _ := strconv.Atoi(r.URL.Query().Get("pageSize"))
		query := r.URL.Query().Get("q")
		items, total, err := opts.GetCacheEntries(r.Context(), page, pageSize, query)
		if err != nil {
			writeInternalErr(w, r, "cache_query_failed", err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"items":    items,
			"total":    total,
			"page":     page,
			"pageSize": pageSize,
		})
	}
}

// cacheDeleteHandler DELETE /api/cache/entries/:id
func cacheDeleteHandler(opts Options) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !requireAdmin(opts, w, r) {
			return
		}
		if opts.DeleteCacheEntry == nil {
			writeError(w, http.StatusServiceUnavailable, "cache_unavailable", "cache not configured")
			return
		}
		// chi.URLParam 拿 :id
		idStr := chi.URLParam(r, "id")
		id, err := strconv.ParseInt(idStr, 10, 64)
		if err != nil || id <= 0 {
			writeError(w, http.StatusBadRequest, "invalid_id", "id must be a positive integer")
			return
		}
		if err := opts.DeleteCacheEntry(r.Context(), id); err != nil {
			writeInternalErr(w, r, "cache_delete_failed", err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"id":      id,
			"deleted": true,
		})
	}
}

// cleanupTasksHandler GET /api/cleanup/tasks
func cleanupTasksHandler(opts Options) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if opts.ListCleanupTasks == nil {
			writeError(w, http.StatusServiceUnavailable, "cleanup_unavailable", "cleanup not configured")
			return
		}
		tasks, err := opts.ListCleanupTasks(r.Context())
		if err != nil {
			writeInternalErr(w, r, "cleanup_query_failed", err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"items": tasks,
			"total": len(tasks),
		})
	}
}

// runCleanupHandler POST /api/cleanup/tasks/:id/run
//
// 立即跑一次清理任务（不等 cron）。
func runCleanupHandler(opts Options) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !requireAdmin(opts, w, r) {
			return
		}
		if opts.RunCleanupTask == nil {
			writeError(w, http.StatusServiceUnavailable, "cleanup_unavailable", "cleanup not configured")
			return
		}
		idStr := chi.URLParam(r, "id")
		id, err := strconv.ParseInt(idStr, 10, 64)
		if err != nil || id <= 0 {
			writeError(w, http.StatusBadRequest, "invalid_id", "id must be a positive integer")
			return
		}
		report, err := opts.RunCleanupTask(r.Context(), id)
		if err != nil {
			writeInternalErr(w, r, "cleanup_run_failed", err)
			return
		}
		writeJSON(w, http.StatusOK, report)
	}
}

// upstreamHealthHandler GET /api/health/upstream
func upstreamHealthHandler(opts Options) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if opts.GetUpstreamHealth == nil {
			writeError(w, http.StatusServiceUnavailable, "upstream_health_unavailable", "upstream health not configured")
			return
		}
		writeJSON(w, http.StatusOK, opts.GetUpstreamHealth())
	}
}

// accessLogsHandler GET /api/logs?page=1&pageSize=50
func accessLogsHandler(opts Options) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if opts.GetAccessLogs == nil {
			writeError(w, http.StatusServiceUnavailable, "logs_unavailable", "access logs not configured")
			return
		}
		page, _ := strconv.Atoi(r.URL.Query().Get("page"))
		pageSize, _ := strconv.Atoi(r.URL.Query().Get("pageSize"))
		logs, total, err := opts.GetAccessLogs(r.Context(), page, pageSize)
		if err != nil {
			writeInternalErr(w, r, "logs_query_failed", err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"items":    logs,
			"total":    total,
			"page":     page,
			"pageSize": pageSize,
		})
	}
}

// dashboardSummaryHandler GET /api/dashboard/summary
func dashboardSummaryHandler(opts Options) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if opts.GetDashboardSummary == nil {
			writeError(w, http.StatusServiceUnavailable, "dashboard_unavailable", "dashboard not configured")
			return
		}
		s, err := opts.GetDashboardSummary(r.Context())
		if err != nil {
			writeInternalErr(w, r, "dashboard_query_failed", err)
			return
		}
		writeJSON(w, http.StatusOK, s)
	}
}
