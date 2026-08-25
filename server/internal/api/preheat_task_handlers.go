package api

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/cncachehub/server/internal/storage"
	"github.com/go-chi/chi/v5"
)

// PreheatTaskResponse 是 list / get 返回结构。
type PreheatTaskResponse struct {
	Items []storage.PreheatTask `json:"items"`
	Total int                   `json:"total"`
}

type preheatTaskCreateRequest struct {
	Name           string   `json:"name"`
	Kind           string   `json:"kind"`
	Targets        []string `json:"targets"`
	CronExpression string   `json:"cronExpression"`
	MaxRetries     int      `json:"maxRetries"`
	Enabled        *bool    `json:"enabled,omitempty"`
}

// preheatTaskListHandler GET /api/preheat/tasks
func preheatTaskListHandler(opts Options) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		items, err := opts.ListPreheatTasks(r.Context())
		if err != nil {
			writeInternalErr(w, r, "preheat_tasks_list_failed", err)
			return
		}
		writeJSON(w, http.StatusOK, PreheatTaskResponse{Items: items, Total: len(items)})
	}
}

// preheatTaskCreateHandler POST /api/preheat/tasks（admin）
func preheatTaskCreateHandler(opts Options) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !requireAdmin(opts, w, r) {
			return
		}
		var req preheatTaskCreateRequest
		if !decodeJSONBody(w, r, &req) {
			return
		}
		if strings.TrimSpace(req.Name) == "" {
			writeError(w, http.StatusBadRequest, "invalid_name", "name required")
			return
		}
		if req.Kind != "docker" && req.Kind != "steam" && req.Kind != "resource" && req.Kind != "huggingface_model" {
			writeError(w, http.StatusBadRequest, "invalid_kind", "kind must be 'docker' | 'steam' | 'resource' | 'huggingface_model'")
			return
		}
		// 去空 + 去重（trim 后比较）
		seen := map[string]bool{}
		cleaned := make([]string, 0, len(req.Targets))
		for _, t := range req.Targets {
			t = strings.TrimSpace(t)
			if t == "" {
				continue
			}
			if seen[t] {
				continue
			}
			seen[t] = true
			cleaned = append(cleaned, t)
		}
		if len(cleaned) == 0 {
			writeError(w, http.StatusBadRequest, "empty_targets", "at least 1 target required")
			return
		}
		enabled := true
		if req.Enabled != nil {
			enabled = *req.Enabled
		}
		task, err := opts.CreatePreheatTask(r.Context(), storage.PreheatTask{
			Name: req.Name, Kind: req.Kind, Targets: cleaned,
			CronExpression: req.CronExpression, MaxRetries: req.MaxRetries, Enabled: enabled,
		})
		if err != nil {
			writeInternalErr(w, r, "preheat_task_create_failed", err)
			return
		}
		writeJSON(w, http.StatusCreated, task)
	}
}

// preheatTaskDeleteHandler DELETE /api/preheat/tasks/:id（admin）
func preheatTaskDeleteHandler(opts Options) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !requireAdmin(opts, w, r) {
			return
		}
		id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
		if err != nil || id <= 0 {
			writeError(w, http.StatusBadRequest, "invalid_id", "id must be positive integer")
			return
		}
		if err := opts.DeletePreheatTask(r.Context(), id); err != nil {
			writeInternalErr(w, r, "preheat_task_delete_failed", err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// preheatTaskRunHandler POST /api/preheat/tasks/:id/run（admin）
// 触发预热。返回 202 + 任务当前状态（goroutine 异步执行）。
func preheatTaskRunHandler(opts Options) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !requireAdmin(opts, w, r) {
			return
		}
		id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
		if err != nil || id <= 0 {
			writeError(w, http.StatusBadRequest, "invalid_id", "id must be positive integer")
			return
		}
		if err := opts.RunPreheatTask(r.Context(), id); err != nil {
			writeError(w, http.StatusConflict, "preheat_task_run_failed", err.Error())
			return
		}
		task, err := opts.GetPreheatTask(r.Context(), id)
		if err != nil {
			writeInternalErr(w, r, "preheat_task_get_failed", err)
			return
		}
		writeJSON(w, http.StatusAccepted, task)
	}
}

// preheatTaskCancelHandler POST /api/preheat/tasks/:id/cancel（admin）
// 取消正在跑的任务。
func preheatTaskCancelHandler(opts Options) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !requireAdmin(opts, w, r) {
			return
		}
		id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
		if err != nil || id <= 0 {
			writeError(w, http.StatusBadRequest, "invalid_id", "id must be positive integer")
			return
		}
		if !opts.CancelPreheatTask(id) {
			writeError(w, http.StatusConflict, "not_running", "task is not currently running")
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// preheatTaskItemsHandler GET /api/preheat/tasks/:id/items
// 拿任务下每条 target 的执行状态。
func preheatTaskItemsHandler(opts Options) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
		if err != nil || id <= 0 {
			writeError(w, http.StatusBadRequest, "invalid_id", "id must be positive integer")
			return
		}
		items, err := opts.ListPreheatItems(r.Context(), id)
		if err != nil {
			writeInternalErr(w, r, "preheat_items_list_failed", err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"items": items, "total": len(items)})
	}
}
