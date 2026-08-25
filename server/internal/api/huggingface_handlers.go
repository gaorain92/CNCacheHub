// Package api: Hugging Face 模型加速（独立菜单）相关 endpoint。
//
//   - GET  /api/huggingface/models/{modelId}/tree?revision=main
//     → 透传 huggingface.co/api/models/{modelId}/tree/{revision}（按需带 token）
//     → 返回文件树（path / size / type），供前端展示 + 勾选用
//
//   - POST /api/huggingface/preheat
//     → 入参 { modelId, revision, patterns?, maxFiles? }
//     → 后端自己再拉一次 tree（避免前端伪造文件列表），按 patterns 过滤后建
//       preheat task (kind=huggingface_model)，targets 形如 "<id>|<rev>|<file>"
//
// 设计要点：
//   - tree / preheat 都用同一份 GetHuggingFaceToken 注入 Authorization（gated 模型）
//   - 不在前端校验 HF token（避免把 token 经前端 round-trip 漏出来）
//   - 任何外部请求必须 10s 内连上游（防止阻塞 API handler goroutine）
//   - FetchHuggingFaceTree 通过 Options 注入；main.go 注入真实现，测试可注入 fake
package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/cncachehub/server/internal/storage"
	"github.com/go-chi/chi/v5"
)

// huggingFaceFile 是 tree API 返回的单条记录（HF 公开 API schema 摘录）。
type huggingFaceFile struct {
	Type string `json:"type"`           // "file" | "directory"
	Path string `json:"path"`           // 相对 model root 的路径
	Size int64  `json:"size,omitempty"` // bytes；directory 无此字段
	OID  string `json:"oid,omitempty"`  // blob/lfs oid
}

// huggingFaceTreeResponse 是 GET /api/huggingface/models/{id}/tree 的返回。
type huggingFaceTreeResponse struct {
	ModelID  string            `json:"modelId"`
	Revision string            `json:"revision"`
	Files    []huggingFaceFile `json:"files"`
	Total    int               `json:"total"`
}

// huggingFaceTreeHandler GET /api/huggingface/models/*
//
// chi 的 {name:regex} 不跨 "/" 匹配，所以用 wildcard "*" 兜底。路径形如：
//   /api/huggingface/models/<org>/<name>[/...]/tree
// 我们手动剥掉前缀 + 末尾 "/tree" 拿 modelId。
func huggingFaceTreeHandler(opts Options) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// chi.URLParam(r, "*") 返去掉前导 "/" 的剩余路径
		rest := chi.URLParam(r, "*")
		// 期望 suffix "/tree" — 剥掉
		const treeSuffix = "/tree"
		if !strings.HasSuffix(rest, treeSuffix) {
			writeError(w, http.StatusBadRequest, "bad_path", "expected path ending with /tree")
			return
		}
		modelID := strings.TrimSuffix(rest, treeSuffix)
		// 去掉前导 "/"
		modelID = strings.TrimPrefix(modelID, "/")
		if modelID == "" {
			writeError(w, http.StatusBadRequest, "missing_model_id", "modelId is required")
			return
		}
		if strings.ContainsAny(modelID, " \t\n") {
			writeError(w, http.StatusBadRequest, "invalid_model_id", "modelId must not contain whitespace")
			return
		}
		revision := strings.TrimSpace(r.URL.Query().Get("revision"))
		if revision == "" {
			revision = "main"
		}
		files, err := opts.FetchHuggingFaceTree(r.Context(), modelID, revision)
		if err != nil {
			// 把 HF 真实错误透传出去；只在 "有 token 但仍 401/403" 时提示 gated。
			// 没配 token 时返 401/403 也可能是 rate limit（HF 突发限流）—
			// 直接说 "HF 返回 401/403" 让用户自己判断。
			status := http.StatusBadGateway
			code := "hf_tree_fetch_failed"
			msg := err.Error()
			if errors.Is(err, errHFAuthRequired) {
				// 透传 HF 的 status code
				status = http.StatusForbidden
				code = "hf_token_required"
				hasToken := opts.GetHuggingFaceToken != nil && strings.TrimSpace(opts.GetHuggingFaceToken()) != ""
				if hasToken {
					msg = "HF returned 401/403 even with token configured — model is gated for this account (check https://huggingface.co/settings/gated repos)"
				} else {
					msg = "HF returned 401/403 — could be: gated model (set token in Settings), rate limited (wait and retry), or temporary HF outage. Last error: " + hfLastError(err)
				}
			}
			writeError(w, status, code, msg)
			return
		}
		// 过滤 directory + 空 path（fetcher 也过滤，但兜底）
		out := make([]huggingFaceFile, 0, len(files))
		for _, f := range files {
			if f.Type != "file" || strings.TrimSpace(f.Path) == "" {
				continue
			}
			out = append(out, f)
		}
		writeJSON(w, http.StatusOK, huggingFaceTreeResponse{
			ModelID: modelID, Revision: revision, Files: out, Total: len(out),
		})
	}
}

// errHFAuthRequired 标记 HF 返回 401/403 的场景。
//
// 可能是 gated 模型（无 token 或 token 没权限），也可能是 rate limit。
// 原始 HF 错误消息包在 hfAuthError 里，可通过 errors.As 拿。
type hfAuthError struct {
	Status int    // HF 返回的 status
	Body   string // HF 响应 body（截断到 512B）
}

func (e *hfAuthError) Error() string {
	if e.Body != "" {
		return fmt.Sprintf("HF returned %d: %s", e.Status, e.Body)
	}
	return fmt.Sprintf("HF returned %d", e.Status)
}

var _ error = (*hfAuthError)(nil)

// errHFAuthRequired 是哨兵值，handler 用来识别"需要鉴权"分支。
// 真信息走 hfAuthError。但 errors.Is(err, errHFAuthRequired) 仍然成立
// （errors.Is 默认走 == 比较，指针类型 → 需要自己实现 Is）。
func (e *hfAuthError) Is(target error) bool {
	return target == errHFAuthRequired
}

// errHFAuthRequired 哨兵（handler 用 errors.Is 判定）
var errHFAuthRequired = errors.New("huggingface auth required")

// escapeHFPathSegment URL-encode 一个 path segment，但保留 "/" 不动。
//
// HF model id 含 "/"（如 "Qwen/Qwen2.5-1.5B-Instruct"），如果用
// url.PathEscape 会把 "/" 编码成 "%2F"，HF API 直接 400 拒绝。
// 只 encode 真正非法的字符：空白、控制字符、? # & 等。
func escapeHFPathSegment(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r == '/':
			b.WriteRune(r)
		case r == ' ' || r == '\t' || r == '\n' || r == '\r':
			// handler 已拦过，但兜底
			b.WriteString("%20")
		case r == '?' || r == '#' || r == '&' || r == '=' || r == '%':
			// 这些字符必须 encode（否则破坏 URL 结构）
			b.WriteString(url.QueryEscape(string(r)))
		case r < 0x20 || r == 0x7F:
			// 控制字符
			b.WriteString("%" + fmt.Sprintf("%02X", r))
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// defaultHFUpstream 供 main.go 注入或测试使用。
const defaultHFUpstream = "https://huggingface.co"

// hfLastError 从错误链里提取 HF 真实 body（如果错误来自 hfAuthError）。
// 用来在没 token 401/403 时给用户更具体的提示，而不是只说 "gated model"。
func hfLastError(err error) string {
	var hf *hfAuthError
	if errors.As(err, &hf) && hf.Body != "" {
		return hf.Body
	}
	return err.Error()
}

// RealFetchHuggingFaceTree 构造真实现：拉 HF tree API 一次（带 timeout），过滤 directory。
// 注入到 Options.FetchHuggingFaceTree 后由 handler 调用。
//
// getToken 闭包返 HF token（空 = 不注入 Authorization）。main.go 注入
// db-backed 实现；测试用 nil。
func RealFetchHuggingFaceTree(getToken func() string) func(ctx context.Context, modelID, revision string) ([]huggingFaceFile, error) {
	return func(parent context.Context, modelID, revision string) ([]huggingFaceFile, error) {
		ctx, cancel := context.WithTimeout(parent, 10*time.Second)
		defer cancel()
		// HF model id 形如 "org/name"（含 "/"），revision 是 ref；
		// url.PathEscape 会把 "/" 编码成 "%2F" 导致 HF 拒绝。
		// 只 escape 非 "/" 的非法字符（modelId handler 已拦了 whitespace / 控制字符）。
		upstream := defaultHFUpstream + "/api/models/" + escapeHFPathSegment(modelID) + "/tree/" + escapeHFPathSegment(revision)
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, upstream, nil)
		if err != nil {
			return nil, fmt.Errorf("build request: %w", err)
		}
		req.Header.Set("Accept", "application/json")
		req.Header.Set("User-Agent", "cncachehub/1.0")
		if getToken != nil {
			if tok := strings.TrimSpace(getToken()); tok != "" {
				req.Header.Set("Authorization", "Bearer "+tok)
			}
		}
		client := &http.Client{Timeout: 8 * time.Second}
		resp, err := client.Do(req)
		if err != nil {
			return nil, fmt.Errorf("get %s: %w", upstream, err)
		}
		defer resp.Body.Close()
		if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
			body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
			return nil, &hfAuthError{Status: resp.StatusCode, Body: string(body)}
		}
		if resp.StatusCode == http.StatusNotFound {
			return nil, fmt.Errorf("model or revision not found (HTTP 404): %s/%s", modelID, revision)
		}
		if resp.StatusCode >= 400 {
			body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
			return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(body))
		}
		// 限制 response size（防 OOM — 一个 model 不可能 16MB 树）
		raw, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
		if err != nil {
			return nil, fmt.Errorf("read body: %w", err)
		}
		var files []huggingFaceFile
		if err := json.Unmarshal(raw, &files); err != nil {
			return nil, fmt.Errorf("decode tree: %w", err)
		}
		// 过滤 directory；空 path 兜底
		out := make([]huggingFaceFile, 0, len(files))
		for _, f := range files {
			if f.Type != "file" {
				continue
			}
			if strings.TrimSpace(f.Path) == "" {
				continue
			}
			out = append(out, f)
		}
		return out, nil
	}
}

// === preheat 创建 ===

// huggingFacePreheatRequest POST /api/huggingface/preheat 的入参。
type huggingFacePreheatRequest struct {
	ModelID  string   `json:"modelId"`            // 必填
	Revision string   `json:"revision,omitempty"` // 默认 "main"
	Patterns []string `json:"patterns,omitempty"` // glob 模式；空 = 全部
	MaxFiles int      `json:"maxFiles,omitempty"` // 兜底上限；0 = 1000
	Name     string   `json:"name,omitempty"`     // task 名；空 = 自动生成
	Force    bool     `json:"force,omitempty"`    // 绕过 cache cap 检查（admin 确认后用）
}

// huggingFacePreheatResponse POST /api/huggingface/preheat 的返回。
//
// 当 total size 超过 cache cap 且 !Force 时，task 字段为空、refused=true。
// 客户端应该用 refusedWhy + bytesTotal 给用户展示具体决策。
type huggingFacePreheatResponse struct {
	Task         storage.PreheatTask `json:"task"`
	TotalFiles   int                 `json:"totalFiles"`
	Filtered     int                 `json:"filteredFiles"`
	Skipped      int                 `json:"skippedFiles"`
	Truncated    bool                `json:"truncated"`
	BytesTotal   int64               `json:"bytesTotal"`
	Refused      bool                `json:"refused,omitempty"`      // true = 因 cache cap 拒绝
	RefusedWhy   string              `json:"refusedWhy,omitempty"`   // 拒绝原因（人类可读）
	CacheCapGB   int                 `json:"cacheCapGb,omitempty"`   // 当前 cap，方便前端展示
}

// huggingFacePreheatHandler POST /api/huggingface/preheat（admin）
//
// 流程：
//  1. 后端自己再调一次 tree API（不信任前端传 file 列表 — 避免伪造触发任意下载）
//  2. 按 patterns 过滤（支持简单 glob："*.safetensors"、"config.*"、路径前缀 "onnx/"）
//  3. 按 MaxFiles 截断（防单次预热过载）
//  4. 创建一个 preheat task（kind=huggingface_model），targets 形如 "<id>|<rev>|<file>"
//  5. 返回 task + 文件统计
func huggingFacePreheatHandler(opts Options) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !requireAdmin(opts, w, r) {
			return
		}
		var req huggingFacePreheatRequest
		if !decodeJSONBody(w, r, &req) {
			return
		}
		modelID := strings.TrimSpace(req.ModelID)
		if modelID == "" {
			writeError(w, http.StatusBadRequest, "missing_model_id", "modelId is required")
			return
		}
		revision := strings.TrimSpace(req.Revision)
		if revision == "" {
			revision = "main"
		}
		maxFiles := req.MaxFiles
		if maxFiles <= 0 {
			maxFiles = 1000
		}
		files, err := opts.FetchHuggingFaceTree(r.Context(), modelID, revision)
		if err != nil {
			status := http.StatusBadGateway
			code := "hf_tree_fetch_failed"
			if errors.Is(err, errHFAuthRequired) {
				// 用 403 而非 401：避免前端全局拦截器误判为 session 失效
				status = http.StatusForbidden
				code = "hf_token_required"
			}
			writeError(w, status, code, err.Error())
			return
		}
		// 过滤
		var matched []huggingFaceFile
		var skipped int
		if len(req.Patterns) == 0 {
			matched = files
		} else {
			// 编译失败当无效模式处理（跳过）
			matchers := make([]func(string) bool, 0, len(req.Patterns))
			for _, p := range req.Patterns {
				fn, err := compileHFPattern(p)
				if err != nil {
					skipped++
					continue
				}
				matchers = append(matchers, fn)
			}
			for _, f := range files {
				if anyMatch(matchers, f.Path) {
					matched = append(matched, f)
				}
			}
		}
		// 截断
		truncated := false
		if len(matched) > maxFiles {
			matched = matched[:maxFiles]
			truncated = true
		}
		// 构造 targets
		targets := make([]string, 0, len(matched))
		var bytesTotal int64
		for _, f := range matched {
			targets = append(targets, modelID+"|"+revision+"|"+f.Path)
			bytesTotal += f.Size
		}
		if len(targets) == 0 {
			writeError(w, http.StatusBadRequest, "no_files_matched", "no files matched the patterns")
			return
		}
		// 缓存上限检查：硬阻止超出 cacheTotalGb 的预热。
		// 用户在请求里 force=true 可绕过（小 VPS / cacheTotalGB 调小等场景下
		// 也可能想强行全量，但默认安全）。
		if !req.Force && opts.GetSettings != nil {
			settings, sErr := opts.GetSettings(r.Context())
			if sErr == nil && settings.CacheTotalGB > 0 {
				capBytes := int64(settings.CacheTotalGB) * (1 << 30) // GB → bytes
				if bytesTotal > capBytes {
					writeJSON(w, http.StatusOK, huggingFacePreheatResponse{
						Refused:    true,
						RefusedWhy: fmt.Sprintf("estimated size %s exceeds cache cap %s (set in Settings.cacheTotalGb, raise it or use patterns to filter smaller files)", formatBytesShort(bytesTotal), formatBytesShort(capBytes)),
						BytesTotal: bytesTotal,
						TotalFiles: len(files),
						Filtered:   len(matched),
						CacheCapGB: settings.CacheTotalGB,
					})
					return
				}
			}
		}
		// task name 兜底
		taskName := strings.TrimSpace(req.Name)
		if taskName == "" {
			taskName = "hf: " + modelID + " @ " + revision
		}
		task, err := opts.CreatePreheatTask(r.Context(), storage.PreheatTask{
			Name: taskName, Kind: storage.PreheatKindHuggingFaceModel, Targets: targets, Enabled: true,
		})
		if err != nil {
			writeInternalErr(w, r, "hf_preheat_create_failed", err)
			return
		}
		resp := huggingFacePreheatResponse{
			Task:       task,
			TotalFiles: len(files),
			Filtered:   len(matched),
			Skipped:    skipped,
			Truncated:  truncated,
			BytesTotal: bytesTotal,
		}
		// 把当前 cap 顺手返回给前端（节省一次 /api/settings 调用）
		if opts.GetSettings != nil {
			if s, err := opts.GetSettings(r.Context()); err == nil {
				resp.CacheCapGB = s.CacheTotalGB
			}
		}
		writeJSON(w, http.StatusCreated, resp)
	}
}

// formatBytesShort 跟 HuggingFaceView.formatBytes 一致；放 server 端用。
func formatBytesShort(n int64) string {
	const k = int64(1024)
	if n < k {
		return fmt.Sprintf("%d B", n)
	}
	if n < k*k {
		return fmt.Sprintf("%.1f KB", float64(n)/float64(k))
	}
	if n < k*k*k {
		return fmt.Sprintf("%.1f MB", float64(n)/float64(k*k))
	}
	return fmt.Sprintf("%.2f GB", float64(n)/float64(k*k*k))
}

// anyMatch 至少一个 matcher 命中即 true。
func anyMatch(matchers []func(string) bool, name string) bool {
	for _, m := range matchers {
		if m(name) {
			return true
		}
	}
	return false
}

// compileHFPattern 把一个简单 glob 编译成 matcher。
//
// 支持：
//   - "*"           — 任意非空 basename（不含路径分隔符）
//   - "*.ext"       — basename 以后缀结尾
//   - "prefix*"     — basename 以前缀开头
//   - "dir/*"       — 路径前缀
//   - "exact.json"  — 精确匹配 basename
//
// 不支持：多级 `**`、字符类 `[abc]`、转义。
func compileHFPattern(pat string) (func(string) bool, error) {
	pat = strings.TrimSpace(pat)
	if pat == "" {
		return nil, errors.New("empty pattern")
	}
	// 全 "*" 匹配所有
	if pat == "*" {
		return func(string) bool { return true }, nil
	}
	// 路径前缀：以 "/" 结尾 → 必须以前缀开头
	if strings.HasSuffix(pat, "/") {
		prefix := pat
		return func(p string) bool { return strings.HasPrefix(p, prefix) }, nil
	}
	// 含 "/" 且不以 "*" 开头：当作路径前缀（不带斜杠后缀也算 dir 前缀）
	if strings.Contains(pat, "/") && !strings.HasPrefix(pat, "*") {
		prefix := pat
		return func(p string) bool { return strings.HasPrefix(p, prefix) }, nil
	}
	// basename glob
	if strings.HasPrefix(pat, "*") {
		suffix := pat[1:]
		return func(p string) bool {
			base := p
			if i := strings.LastIndex(p, "/"); i >= 0 {
				base = p[i+1:]
			}
			return strings.HasSuffix(base, suffix)
		}, nil
	}
	if strings.HasSuffix(pat, "*") {
		prefix := pat[:len(pat)-1]
		return func(p string) bool {
			base := p
			if i := strings.LastIndex(p, "/"); i >= 0 {
				base = p[i+1:]
			}
			return strings.HasPrefix(base, prefix)
		}, nil
	}
	if strings.Contains(pat, "*") {
		// 简单 "*xxx*yyy*" 中间匹配
		parts := strings.Split(pat, "*")
		return func(p string) bool {
			base := p
			if i := strings.LastIndex(p, "/"); i >= 0 {
				base = p[i+1:]
			}
			idx := 0
			for _, part := range parts {
				if part == "" {
					continue
				}
				j := strings.Index(base[idx:], part)
				if j < 0 {
					return false
				}
				idx += j + len(part)
			}
			return true
		}, nil
	}
	// 精确 basename 匹配
	return func(p string) bool {
		base := p
		if i := strings.LastIndex(p, "/"); i >= 0 {
			base = p[i+1:]
		}
		return base == pat
	}, nil
}
