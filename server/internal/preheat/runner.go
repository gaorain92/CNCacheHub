// Package preheat 提供预热任务执行逻辑（PRD §9.2.3 / §9.3.3 / §9.5.5）。
//
// 触发方式：
//   - Docker: HTTP GET /v2/<repo>/manifests/<ref> 触发 proxy 拉取 manifest；
//     然后逐个 GET /v2/<repo>/blobs/<digest> 触发 layer 落盘缓存。
//   - Steam: exec.CommandContext('docker run cm2network/steamcmd ... +app_update')。
//   - Resource: 暂未实现（返回 error）。
//
// 进度写入 preheat_items + preheat_tasks。
package preheat

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/cncachehub/server/internal/storage"
)

// Runner 持有预热执行所需依赖。
type Runner struct {
	DB          *storage.DB
	CNCHBaseURL string // 形如 "http://127.0.0.1:8082"
	HTTPClient  *http.Client
	Log         *slog.Logger

	mu      sync.Mutex
	running map[int64]context.CancelFunc // taskID → cancel
}

// NewRunner 构造 runner。
func NewRunner(db *storage.DB, cnchBaseURL string, log *slog.Logger) *Runner {
	if log == nil {
		log = slog.Default()
	}
	return &Runner{
		DB:          db,
		CNCHBaseURL: strings.TrimRight(cnchBaseURL, "/"),
		HTTPClient:  &http.Client{Timeout: 30 * time.Minute},
		Log:         log,
		running:     make(map[int64]context.CancelFunc),
	}
}

// RunTask 异步跑一个任务。返回 immediately；进度通过 storage 写入。
func (r *Runner) RunTask(parent context.Context, taskID int64) error {
	task, err := r.DB.GetPreheatTask(parent, taskID)
	if err != nil {
		return err
	}
	if task.Status == storage.PreheatStatusRunning {
		return errors.New("preheat: task already running")
	}
	ctx, cancel := context.WithCancel(context.Background())
	r.mu.Lock()
	r.running[taskID] = cancel
	r.mu.Unlock()

	go func() {
		defer func() {
			r.mu.Lock()
			delete(r.running, taskID)
			r.mu.Unlock()
		}()
		r.runTaskInner(ctx, task)
	}()
	return nil
}

// CancelTask 取消正在跑的任务。
func (r *Runner) CancelTask(taskID int64) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	cancel, ok := r.running[taskID]
	if !ok {
		return false
	}
	cancel()
	return true
}

// runTaskInner 实际执行入口（goroutine）。
//
// ctx 是 cancellable 的（用户 CancelTask 会触发），仅用于：
//   - 中断 HTTP/执行循环
//   - 判断循环是否被取消
//
// DB 写（UpdatePreheatTaskStatus / UpdatePreheatItem / UpdatePreheatTaskProgress）
// 一律用独立的 statusCtx，避免 ctx 被 cancel 后状态卡在 "running"。
func (r *Runner) runTaskInner(ctx context.Context, task storage.PreheatTask) {
	start := time.Now()
	statusCtx, statusCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer statusCancel()

	if err := r.DB.UpdatePreheatTaskStatus(statusCtx, task.ID, storage.PreheatStatusRunning, "", 0); err != nil {
		r.Log.Error("preheat: mark running failed", "task_id", task.ID, "err", err)
		return
	}
	items, err := r.DB.ListPreheatItems(statusCtx, task.ID)
	if err != nil {
		r.markTaskError(statusCtx, task.ID, "list items: "+err.Error(), time.Since(start).Milliseconds())
		return
	}

	var firstErrMsg string
	for _, it := range items {
		if ctx.Err() != nil {
			firstErrMsg = "canceled"
			break
		}
		if err := r.runItem(ctx, task, it); err != nil {
			r.Log.Warn("preheat: item failed", "task_id", task.ID, "target", it.Target, "err", err)
			if firstErrMsg == "" {
				firstErrMsg = fmt.Sprintf("item %q failed: %v", it.Target, err)
			}
		}
	}

	dur := time.Since(start).Milliseconds()
	finalStatus := storage.PreheatStatusDone
	finalErr := ""
	if firstErrMsg != "" {
		finalStatus = storage.PreheatStatusError
		finalErr = firstErrMsg
	}
	if ctx.Err() != nil {
		finalStatus = storage.PreheatStatusCanceled
		finalErr = "canceled by user"
	}
	if err := r.DB.UpdatePreheatTaskStatus(statusCtx, task.ID, finalStatus, finalErr, dur); err != nil {
		r.Log.Error("preheat: mark final status failed", "task_id", task.ID, "err", err)
	}
}

func (r *Runner) markTaskError(ctx context.Context, taskID int64, msg string, dur int64) {
	_ = r.DB.UpdatePreheatTaskStatus(ctx, taskID, storage.PreheatStatusError, msg, dur)
}

// runItem 跑单条 target；返回 error 不致命（仅写 item status = error），由 caller 聚合。
//
// ctx 用作 HTTP/执行的可取消上下文；DB 写用独立的 writeCtx，避免 cancel 阻断状态落地。
func (r *Runner) runItem(ctx context.Context, task storage.PreheatTask, item storage.PreheatItem) error {
	startedAt := time.Now().Unix()
	writeCtx, writeCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer writeCancel()
	_ = r.DB.UpdatePreheatItem(writeCtx, item.ID, storage.PreheatItemRunning, "", 0, startedAt, 0)

	var (
		bytesAdded int64
		err        error
	)
	switch task.Kind {
	case storage.PreheatKindDocker:
		bytesAdded, err = r.runDockerImage(ctx, item.Target)
	case storage.PreheatKindSteam:
		err = r.runSteamAppID(ctx, item.Target)
	case storage.PreheatKindResource:
		err = errors.New("resource preheat not yet implemented (planned P2)")
	default:
		err = fmt.Errorf("unknown kind: %s", task.Kind)
	}

	finishedAt := time.Now().Unix()
	finalStatus := storage.PreheatItemDone
	finalErr := ""
	if err != nil {
		finalStatus = storage.PreheatItemError
		finalErr = err.Error()
	}
	if updateErr := r.DB.UpdatePreheatItem(writeCtx, item.ID, finalStatus, finalErr, bytesAdded, startedAt, finishedAt); updateErr != nil {
		r.Log.Error("preheat: update item", "err", updateErr)
	}
	// 累加 task 进度（成功和失败都算 1 done）
	if updateErr := r.DB.UpdatePreheatTaskProgress(writeCtx, task.ID, 1, bytesAdded); updateErr != nil {
		r.Log.Error("preheat: update task progress", "err", updateErr)
	}
	return err
}

// runDockerImage 拉镜像 manifest + 各 layer（通过 CNCacheHub 自身 /v2/* 触发缓存）。
// 走内部 URL：127.0.0.1:8082 接收请求 → proxy 拉上游 + 落盘。
func (r *Runner) runDockerImage(ctx context.Context, target string) (int64, error) {
	// target 形如 "nginx:alpine" / "ghcr.io/owner/repo:tag" / "quay.io/prometheus/prometheus:v2.52.0"
	// 拆 registry + name + ref
	registry, name, ref, err := splitDockerImage(target)
	if err != nil {
		return 0, err
	}
	// GET manifest
	manifestPath := dockerPathFor(registry, name, "manifests", ref)
	manifestURL := r.CNCHBaseURL + manifestPath
	req, err := http.NewRequestWithContext(ctx, "GET", manifestURL, nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Accept", "application/vnd.oci.image.manifest.v1+json, application/vnd.docker.distribution.manifest.v2+json, application/vnd.oci.image.index.v1+json")
	resp, err := r.HTTPClient.Do(req)
	if err != nil {
		return 0, fmt.Errorf("get manifest: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return 0, fmt.Errorf("get manifest %s: %d: %s", manifestURL, resp.StatusCode, string(body))
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, err
	}

	// 解析 manifest 拿 layer digests
	digests, err := extractDigests(body, resp.Header.Get("Content-Type"))
	if err != nil {
		return 0, err
	}
	if len(digests) == 0 {
		return 0, errors.New("manifest has no layers/config")
	}

	// 逐个 GET blob
	var totalBytes int64
	for _, dg := range digests {
		blobURL := r.CNCHBaseURL + dockerPathFor(registry, name, "blobs", dg)
		breq, _ := http.NewRequestWithContext(ctx, "GET", blobURL, nil)
		bresp, err := r.HTTPClient.Do(breq)
		if err != nil {
			return totalBytes, fmt.Errorf("get blob %s: %w", dg[:min(20, len(dg))], err)
		}
		// 读取 + 丢弃（proxy 已经把内容写缓存）
		n, _ := io.Copy(io.Discard, bresp.Body)
		_ = bresp.Body.Close()
		if bresp.StatusCode != http.StatusOK {
			return totalBytes, fmt.Errorf("get blob %s: HTTP %d", dg[:min(20, len(dg))], bresp.StatusCode)
		}
		totalBytes += n
	}
	return totalBytes, nil
}

// runSteamAppID 调 docker run cm2network/steamcmd +app_update。
// 阻塞到 docker run 退出。返回 error（含 stdout/stderr 摘要）。
func (r *Runner) runSteamAppID(ctx context.Context, target string) error {
	appID, err := strconv.ParseInt(strings.TrimSpace(target), 10, 64)
	if err != nil || appID <= 0 {
		return fmt.Errorf("invalid steam app id: %q", target)
	}
	if _, lookErr := exec.LookPath("docker"); lookErr != nil {
		return fmt.Errorf("docker not installed on this host: %w", lookErr)
	}
	installDir := fmt.Sprintf("/data/steamapps/%d", appID)
	args := []string{
		"run", "--rm",
		"-v", installDir + ":/steamapps",
		"cm2network/steamcmd:latest",
		"+login", "anonymous",
		"+app_update", strconv.FormatInt(appID, 10),
		"validate",
		"+quit",
	}
	cmd := exec.CommandContext(ctx, "docker", args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		// 截取 stdout 末尾 500 字符做摘要
		s := string(out)
		if len(s) > 500 {
			s = "..." + s[len(s)-500:]
		}
		return fmt.Errorf("docker run failed: %w: %s", err, s)
	}
	return nil
}

// === helpers ===

// splitDockerImage 拆分 "ghcr.io/owner/repo:tag" → registry="ghcr", name="ghcr.io/owner/repo", ref="tag"。
// 短形式 "nginx:alpine" → registry="dockerhub", name="library/nginx", ref="alpine"。
// "nginx" → registry="dockerhub", name="library/nginx", ref="latest"。
func splitDockerImage(target string) (registry, name, ref string, err error) {
	target = strings.TrimSpace(target)
	if target == "" {
		err = errors.New("empty docker image target")
		return
	}
	// digest 形如 name@sha256:abc... — @ 之后的整体作为 ref，不再切
	if at := strings.Index(target, "@"); at > 0 {
		namePart := target[:at]
		ref = target[at+1:]
		registry, name = inferRegistryAndName(namePart)
		if registry == "" {
			err = errors.New("cannot infer registry from target: " + target)
		}
		return
	}
	ref = "latest"
	// 拆 ref
	if i := strings.LastIndex(target, ":"); i > 0 && !strings.Contains(target[i:], "/") {
		ref = target[i+1:]
		target = target[:i]
	}
	registry, name = inferRegistryAndName(target)
	if registry == "" {
		err = errors.New("cannot infer registry from target: " + target)
	}
	return
}

// inferRegistryAndName 从 namePart（无 ref 段）推 registry + 完整 name。
// 规则：含 "." 或 ":" 的第一段当作 registry host；纯段（如 "library/ubuntu"）走 dockerhub。
func inferRegistryAndName(namePart string) (registry, name string) {
	parts := strings.SplitN(namePart, "/", 2)
	if len(parts) == 2 && (strings.Contains(parts[0], ".") || strings.Contains(parts[0], ":") || parts[0] == "localhost") {
		registry = registryNameFromHost(parts[0])
		name = namePart
		return
	}
	// dockerhub 走 library/* 路径；若 user 已显式写 library/，保留
	registry = "dockerhub"
	if strings.HasPrefix(namePart, "library/") {
		name = namePart
	} else {
		name = "library/" + namePart
	}
	return
}

// registryNameFromHost "ghcr.io" → "ghcr", "quay.io" → "quay", "docker.io" → "dockerhub"（特殊）。
// 端口（":5000" 形式）会被剥掉，再走映射/兜底逻辑。
// 与 storage/registry_upstreams.go 里的 name 对齐。
func registryNameFromHost(host string) string {
	if i := strings.Index(host, ":"); i >= 0 {
		host = host[:i]
	}
	switch host {
	case "docker.io", "registry-1.docker.io", "index.docker.io":
		return "dockerhub"
	case "ghcr.io":
		return "ghcr"
	case "quay.io":
		return "quay"
	case "registry.k8s.io":
		return "k8s"
	}
	// 兜底：取第一段作为名字
	if i := strings.Index(host, "."); i > 0 {
		return host[:i]
	}
	return host
}

// dockerPathFor 构造 /v2/<registry>/<name>/<kind>/<ref>。
// registry == "dockerhub" 时不写 "dockerhub/" 段（CNCacheHub 约定）。
func dockerPathFor(registry, name, kind, ref string) string {
	if registry == "dockerhub" {
		return "/v2/" + name + "/" + kind + "/" + ref
	}
	return "/v2/" + registry + "/" + name + "/" + kind + "/" + ref
}

// extractDigests 解析 manifest body 拿所有 blob digests。
// 支持 OCI image manifest / Docker image manifest / OCI image index。
func extractDigests(body []byte, contentType string) ([]string, error) {
	// 简单 OCI / Docker image manifest
	var m struct {
		Config struct {
			Digest string `json:"digest"`
		} `json:"config"`
		Layers []struct {
			Digest string `json:"digest"`
		} `json:"layers"`
		Manifests []struct {
			Digest string `json:"digest"`
		} `json:"manifests"` // OCI image index
	}
	if err := json.Unmarshal(body, &m); err != nil {
		return nil, fmt.Errorf("parse manifest: %w", err)
	}
	var out []string
	if m.Config.Digest != "" {
		out = append(out, m.Config.Digest)
	}
	for _, l := range m.Layers {
		if l.Digest != "" {
			out = append(out, l.Digest)
		}
	}
	for _, mn := range m.Manifests {
		if mn.Digest != "" {
			out = append(out, mn.Digest)
		}
	}
	return out, nil
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
