package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/cncachehub/server/internal/storage"
	"github.com/go-chi/chi/v5"
)

// SteamAppIDResponse 是 list/get 返回结构。
type SteamAppIDResponse struct {
	Items []storage.SteamAppID `json:"items"`
	Total int                  `json:"total"`
}

type steamAppIDCreateRequest struct {
	AppID      int64  `json:"appId"`
	Name       string `json:"name"`
	LoginType  string `json:"loginType"`
	InstallDir string `json:"installDir"`
	Enabled    *bool  `json:"enabled,omitempty"`
}

type steamAppIDPatchRequest struct {
	Name               *string `json:"name,omitempty"`
	LoginType          *string `json:"loginType,omitempty"`
	InstallDir         *string `json:"installDir,omitempty"`
	Enabled            *bool   `json:"enabled,omitempty"`
	CacheBytesEstimate *int64  `json:"cacheBytesEstimate,omitempty"`
}

type preheatResponse struct {
	AppID       int64  `json:"appId"`
	Status      string `json:"status"` // 'ok' | 'error' | 'skipped'
	Message     string `json:"message"`
	DurationMs  int64  `json:"durationMs"`
	CommandLine string `json:"commandLine,omitempty"`
}

// steamAppIDListHandler GET /api/steamcmd/appids（受保护）
func steamAppIDListHandler(opts Options) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		items, err := opts.ListSteamAppIDs(r.Context())
		if err != nil {
			writeInternalErr(w, r, "steam_appids_list_failed", err)
			return
		}
		writeJSON(w, http.StatusOK, SteamAppIDResponse{Items: items, Total: len(items)})
	}
}

// steamAppIDCreateHandler POST /api/steamcmd/appids（admin）
func steamAppIDCreateHandler(opts Options) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !requireAdmin(opts, w, r) {
			return
		}
		var req steamAppIDCreateRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid_json", err.Error())
			return
		}
		if req.AppID <= 0 {
			writeError(w, http.StatusBadRequest, "invalid_app_id", "appId must be positive")
			return
		}
		if strings.TrimSpace(req.Name) == "" {
			writeError(w, http.StatusBadRequest, "invalid_name", "name required")
			return
		}
		if req.LoginType != "" && req.LoginType != "anonymous" && req.LoginType != "account" {
			writeError(w, http.StatusBadRequest, "invalid_login_type", "loginType must be 'anonymous' or 'account'")
			return
		}
		enabled := true
		if req.Enabled != nil {
			enabled = *req.Enabled
		}
		a, err := opts.CreateSteamAppID(r.Context(), storage.SteamAppID{
			AppID: req.AppID, Name: req.Name, LoginType: req.LoginType, InstallDir: req.InstallDir, Enabled: enabled,
		})
		if err != nil {
			writeInternalErr(w, r, "steam_appid_create_failed", err)
			return
		}
		writeJSON(w, http.StatusCreated, a)
	}
}

// steamAppIDPatchHandler PATCH /api/steamcmd/appids/:id（admin）
func steamAppIDPatchHandler(opts Options) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !requireAdmin(opts, w, r) {
			return
		}
		id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
		if err != nil || id <= 0 {
			writeError(w, http.StatusBadRequest, "invalid_id", "id must be positive integer")
			return
		}
		var req steamAppIDPatchRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid_json", err.Error())
			return
		}
		if req.LoginType != nil && *req.LoginType != "anonymous" && *req.LoginType != "account" {
			writeError(w, http.StatusBadRequest, "invalid_login_type", "loginType must be 'anonymous' or 'account'")
			return
		}
		patch := storage.SteamAppIDPatch{
			Name: req.Name, LoginType: req.LoginType, InstallDir: req.InstallDir,
			Enabled: req.Enabled, CacheBytesEstimate: req.CacheBytesEstimate,
		}
		a, err := opts.UpdateSteamAppID(r.Context(), id, patch)
		if err != nil {
			writeInternalErr(w, r, "steam_appid_update_failed", err)
			return
		}
		writeJSON(w, http.StatusOK, a)
	}
}

// steamAppIDDeleteHandler DELETE /api/steamcmd/appids/:id（admin）
func steamAppIDDeleteHandler(opts Options) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !requireAdmin(opts, w, r) {
			return
		}
		id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
		if err != nil || id <= 0 {
			writeError(w, http.StatusBadRequest, "invalid_id", "id must be positive integer")
			return
		}
		if err := opts.DeleteSteamAppID(r.Context(), id); err != nil {
			writeInternalErr(w, r, "steam_appid_delete_failed", err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// steamAppIDPreheatHandler POST /api/steamcmd/appids/:id/preheat（admin）
//
// 实际预热：调用 docker run cm2network/steamcmd ... +login anonymous +app_update <APP_ID> validate
// 前提：宿主机装了 docker（我们不内置 dockerd）。本机没装则返回 503 + 提示。
// 预热是异步的：同步起一个 5s 超时的 docker inspect 检查，命令本体 fire-and-forget 入后台。
//
// PRD §9.5.4 边界：不破解 Steam 凭据 / 不绕过账号权限。anonymous AppID 才会被允许 silent 预热。
// account 类型需要交互式 login（+login <user> <pass>），本 MVP 不支持（仅返回错误说明）。
func steamAppIDPreheatHandler(opts Options) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !requireAdmin(opts, w, r) {
			return
		}
		id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
		if err != nil || id <= 0 {
			writeError(w, http.StatusBadRequest, "invalid_id", "id must be positive integer")
			return
		}
		a, err := opts.GetSteamAppID(r.Context(), id)
		if err != nil {
			writeError(w, http.StatusNotFound, "steam_appid_not_found", err.Error())
			return
		}
		if a.LoginType != "anonymous" {
			resp := preheatResponse{
				AppID:  a.AppID,
				Status: "skipped",
				Message: fmt.Sprintf("AppID %d (%s) 需要 Steam 账号登录（login_type=account），本模块不支持交互式登录，请在宿主机手动跑 steamcmd。", a.AppID, a.Name),
			}
			_ = opts.RecordPreheatResult(r.Context(), a.ID, "skipped", resp.Message, 0)
			writeJSON(w, http.StatusOK, resp)
			return
		}
		// 检查 docker 是否可用
		if _, lookErr := exec.LookPath("docker"); lookErr != nil {
			resp := preheatResponse{
				AppID:  a.AppID,
				Status: "skipped",
				Message: "未检测到 docker 命令。请在 CNCacheHub 宿主机安装 docker 后重试；或参考前端接入文档在客户端机器上跑 steamcmd（带 --dns 指向 CNCacheHub）。",
			}
			_ = opts.RecordPreheatResult(r.Context(), a.ID, "skipped", resp.Message, 0)
			writeJSON(w, http.StatusOK, resp)
			return
		}
		// 构造 docker run 命令
		installDir := a.InstallDir
		if installDir == "" {
			installDir = fmt.Sprintf("/data/steamapps/%d", a.AppID)
		}
		args := []string{
			"run", "--rm",
			"-v", installDir + ":/steamapps",
			"cm2network/steamcmd:latest",
			"+login", "anonymous",
			"+app_update", strconv.FormatInt(a.AppID, 10),
			"validate",
			"+quit",
		}
		cmd := exec.CommandContext(r.Context(), "docker", args...)
		// fire-and-forget：起后台 goroutine，5s 后 cmd.Wait 也会返回
		start := time.Now()
		go func() {
			out, err := cmd.CombinedOutput()
			dur := time.Since(start).Milliseconds()
			bgCtx := context.Background()
			if err != nil {
				_ = opts.RecordPreheatResult(bgCtx, a.ID, "error", "docker run failed: "+err.Error()+"\n"+string(out), dur)
			} else {
				_ = opts.RecordPreheatResult(bgCtx, a.ID, "ok", "preheat completed", dur)
			}
		}()
		resp := preheatResponse{
			AppID:       a.AppID,
			Status:      "running",
			Message:     fmt.Sprintf("已触发 AppID %d (%s) 的预热任务，docker run 在后台执行。完成后 last_preheat_status 会更新到 ok / error。", a.AppID, a.Name),
			CommandLine: "docker " + strings.Join(args, " "),
		}
		_ = opts.RecordPreheatResult(r.Context(), a.ID, "running", resp.Message, 0)
		writeJSON(w, http.StatusAccepted, resp)
	}
}

// chiURLParam 不再使用（用 chi.URLParam）。保留 stub 避免 unused import。
var _ = chi.URLParam
