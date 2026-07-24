// Package api: settings + cleanup dry-run handlers。
package api

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
	"strconv"
)

// settingsGetHandler GET /api/settings
func settingsGetHandler(opts Options) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if opts.GetSettings == nil {
			writeError(w, http.StatusServiceUnavailable, "settings_unavailable", "settings not configured")
			return
		}
		s, err := opts.GetSettings(r.Context())
		if err != nil {
			writeInternalErr(w, r, "settings_query_failed", err)
			return
		}
		writeJSON(w, http.StatusOK, s)
	}
}

// settingsPatchHandler PATCH /api/settings
//
// 仅 admin 可改（PRD 9.7.1）。body 部分字段有效，只改提供的。
func settingsPatchHandler(opts Options) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if opts.UpdateSettings == nil {
			writeError(w, http.StatusServiceUnavailable, "settings_unavailable", "settings not configured")
			return
		}
		u, ok := userFromContext(r.Context())
		if !ok || !u.IsAdmin {
			writeError(w, http.StatusForbidden, "admin_required", "only admin can update settings")
			return
		}
		var patch SettingsPatch
		if err := json.NewDecoder(r.Body).Decode(&patch); err != nil {
			writeError(w, http.StatusBadRequest, "invalid_body", "invalid JSON body")
			return
		}
		// 简单校验
		if patch.CacheTotalGB != nil && *patch.CacheTotalGB < 1 {
			writeError(w, http.StatusBadRequest, "invalid_value", "cacheTotalGb must be >= 1")
			return
		}
		if patch.MaxObjectSizeMB != nil && *patch.MaxObjectSizeMB < 1 {
			writeError(w, http.StatusBadRequest, "invalid_value", "maxObjectSizeMb must be >= 1")
			return
		}
		if patch.ReserveSpaceGB != nil && *patch.ReserveSpaceGB < 1 {
			writeError(w, http.StatusBadRequest, "invalid_value", "reserveSpaceGb must be >= 1")
			return
		}
		if patch.CleanupTriggerPct != nil && (*patch.CleanupTriggerPct < 1 || *patch.CleanupTriggerPct > 99) {
			writeError(w, http.StatusBadRequest, "invalid_value", "cleanupTriggerPct must be in [1, 99]")
			return
		}
		if patch.CleanupTargetPct != nil && (*patch.CleanupTargetPct < 1 || *patch.CleanupTargetPct > 99) {
			writeError(w, http.StatusBadRequest, "invalid_value", "cleanupTargetPct must be in [1, 99]")
			return
		}
		if patch.CleanupTriggerPct != nil && patch.CleanupTargetPct != nil &&
			*patch.CleanupTargetPct >= *patch.CleanupTriggerPct {
			writeError(w, http.StatusBadRequest, "invalid_value", "cleanupTargetPct must be < cleanupTriggerPct")
			return
		}
		updated, err := opts.UpdateSettings(r.Context(), patch, u.ID)
		if err != nil {
			writeInternalErr(w, r, "settings_update_failed", err)
			return
		}
		writeJSON(w, http.StatusOK, updated)
	}
}

// cleanupDryRunHandler POST /api/cleanup/tasks/{id}/dry-run
//
// 跑一次清理预估（不实际删除）。返回预估 freed_count/freed_bytes。
// 让 UI 显示「预计释放 N 条 / X MB」再二次确认。
func cleanupDryRunHandler(opts Options) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !requireAdmin(opts, w, r) {
			return
		}
		if opts.DryRunCleanup == nil {
			writeError(w, http.StatusServiceUnavailable, "cleanup_unavailable", "cleanup not configured")
			return
		}
		idStr := chi.URLParam(r, "id")
		id, err := strconv.ParseInt(idStr, 10, 64)
		if err != nil || id <= 0 {
			writeError(w, http.StatusBadRequest, "invalid_id", "id must be a positive integer")
			return
		}
		report, err := opts.DryRunCleanup(r.Context(), id)
		if err != nil {
			writeInternalErr(w, r, "cleanup_dry_run_failed", err)
			return
		}
		writeJSON(w, http.StatusOK, report)
	}
}
