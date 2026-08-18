// Package proxy 资源加速中心反代（PRD §9.4）
//
// 路由：GET /r/<rule_name>/<path> → fetch <upstream_url>/<path> + 落盘缓存
//
// 设计取舍：
//   - 缓存 key 用 cache.Store 的 (registry=rule_name, repo=path, digest=sha256(path))
//     与 docker blob 自然分开（不同 registry 段）
//   - TTL 通过 resource_cache_entries.expires_at 控制；命中时检查过期
//   - 默认 TTL 24h（rule 可配）；命中 expired 重新拉
//   - 不支持 POST / PUT / DELETE（只 GET）
//   - URL 含敏感参数（?token= / ?signature=）默认不缓存，走旁路
package proxy

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/cncachehub/server/internal/cache"
	"github.com/cncachehub/server/internal/storage"
)

// ResourceHandler 是 /r/* 路由的 http.Handler。
type ResourceHandler struct {
	DB              *storage.DB
	FS              *cache.FileStore
	Log             *slog.Logger
	HTTP            *http.Client
	MaxObjectSize   int64
	CacheReserveGB  int64 // 暂未用（bypass 留给全局策略）

	// GetHuggingFaceToken 返 HF access token（空串 = 未配置）。
	// kind="huggingface_models" 时注入 Authorization: Bearer 头。
	// 不配置（nil）则不注入。PRD §9.4.5
	GetHuggingFaceToken func() string
}

// NewResourceHandler 构造 handler。
func NewResourceHandler(db *storage.DB, fs *cache.FileStore, maxObjectSize int64, log *slog.Logger) *ResourceHandler {
	if log == nil {
		log = slog.Default()
	}
	return &ResourceHandler{
		DB:            db,
		FS:            fs,
		Log:           log,
		HTTP: &http.Client{
			Timeout: 30 * time.Minute,
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				// 跟随上游重定向（github raw → raw.githubusercontent.com）
				if len(via) >= 5 {
					return errors.New("too many redirects")
				}
				return nil
			},
		},
		MaxObjectSize: maxObjectSize,
	}
}

// ServeHTTP 实现 /r/<rule_name>/<rest> 路由。
// 匹配规则：第二段是 rule name，后续段拼到 upstream URL。
func (h *ResourceHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	// 解析 /r/<rule_name>/<rest...>
	rest := strings.TrimPrefix(r.URL.Path, "/r/")
	if rest == "" || rest == r.URL.Path {
		http.Error(w, "bad path", http.StatusBadRequest)
		return
	}
	parts := strings.SplitN(rest, "/", 2)
	ruleName := parts[0]
	restPath := ""
	if len(parts) > 1 {
		restPath = parts[1]
	}
	if ruleName == "" || restPath == "" {
		http.Error(w, "usage: /r/<rule_name>/<path>", http.StatusBadRequest)
		return
	}
	// 不缓存带敏感 query 的 URL
	if hasSensitiveQuery(r.URL.RawQuery) {
		h.serveBypass(w, r, ruleName, restPath, "sensitive_query")
		return
	}

	// 查 rule
	rule, err := h.DB.GetResourceRuleByName(r.Context(), ruleName)
	if err != nil {
		http.Error(w, "rule not found: "+ruleName, http.StatusNotFound)
		return
	}
	if !rule.Enabled {
		http.Error(w, "rule disabled", http.StatusForbidden)
		return
	}
	// P2#1: path_pattern 匹配（白名单更精细）
	if !rule.MatchPath(restPath) {
		http.Error(w, "path not in rule whitelist: "+restPath+" (pattern: "+rule.PathPattern+")", http.StatusForbidden)
		return
	}

	// 查 cache entry
	digest := pathDigest(restPath)
	entry, err := h.DB.GetResourceCacheEntry(r.Context(), rule.ID, restPath)
	cached := err == nil
	expired := cached && entry.ExpiresAt > 0 && entry.ExpiresAt < time.Now().Unix()
	if cached && !expired {
		// HIT — serve from cache
		_ = h.DB.BumpResourceCacheHit(r.Context(), entry.ID)
		if err := h.serveFromFile(w, r, entry, start, rule); err != nil {
			h.Log.Warn("resource: serve cache failed", "rule", ruleName, "path", restPath, "err", err)
			h.serveFromUpstream(w, r, rule, restPath, digest, start)
		}
		return
	}

	// MISS or expired — fetch upstream + cache
	h.serveFromUpstream(w, r, rule, restPath, digest, start)
}

// serveFromUpstream 拉上游 + 落盘 + tee 给 client。
func (h *ResourceHandler) serveFromUpstream(w http.ResponseWriter, r *http.Request, rule storage.ResourceRule, restPath, digest string, start time.Time) {
	upstreamURL := strings.TrimRight(rule.UpstreamURL, "/") + "/" + restPath
	// 拼上 query（透明转发）
	if r.URL.RawQuery != "" {
		upstreamURL += "?" + r.URL.RawQuery
	}
	req, err := http.NewRequestWithContext(r.Context(), "GET", upstreamURL, nil)
	if err != nil {
		http.Error(w, "build upstream request: "+err.Error(), http.StatusInternalServerError)
		return
	}
	// 透传 User-Agent（curl / wget / Playwright 等要识别）
	if ua := r.Header.Get("User-Agent"); ua != "" {
		req.Header.Set("User-Agent", ua)
	} else {
		req.Header.Set("User-Agent", "cncachehub/1.0")
	}
	// Range 透传（断点续传）
	if rng := r.Header.Get("Range"); rng != "" {
		req.Header.Set("Range", rng)
	}
	// HF 模型：注入 Authorization: Bearer <token>（gated 模型需要）
	if rule.Kind == "huggingface_models" {
		if h.GetHuggingFaceToken != nil {
			if tok := strings.TrimSpace(h.GetHuggingFaceToken()); tok != "" {
				req.Header.Set("Authorization", "Bearer "+tok)
			}
		}
	}

	resp, err := h.HTTP.Do(req)
	if err != nil {
		http.Error(w, "upstream fetch: "+err.Error(), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		http.Error(w, fmt.Sprintf("upstream %d: %s", resp.StatusCode, string(body)), resp.StatusCode)
		return
	}

	// 大小校验：超 max_object_size 走 bypass
	cl := resp.ContentLength
	if cl > 0 && h.MaxObjectSize > 0 && cl > h.MaxObjectSize {
		h.Log.Info("resource: bypass (size limit)", "rule", rule.Name, "size", cl)
		// 透传
		w.Header().Set("Content-Type", resp.Header.Get("Content-Type"))
		w.Header().Set("X-CNCacheHub-Bypass", "size_limit")
		w.WriteHeader(resp.StatusCode)
		_, _ = io.Copy(w, resp.Body)
		return
	}

	// 缓存：先写 tmp 文件，再 rename
	cacheDir := filepath.Join(h.FS.RootDir(), "..", "resource", rule.Name)
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		h.Log.Warn("resource: mkdir failed", "err", err)
	}
	tmp, err := os.CreateTemp(cacheDir, "blob-*.tmp")
	if err != nil {
		h.Log.Warn("resource: tmpfile failed", "err", err)
		// fallback: 透传不缓存
		w.Header().Set("Content-Type", resp.Header.Get("Content-Type"))
		w.WriteHeader(resp.StatusCode)
		_, _ = io.Copy(w, resp.Body)
		return
	}
	tmpPath := tmp.Name()
	defer func() {
		_ = tmp.Close()
		_ = os.Remove(tmpPath) // 任何错误都清掉 tmp
	}()

	// tee: 上游 → tmp + client
	w.Header().Set("Content-Type", resp.Header.Get("Content-Type"))
	if resp.ContentLength > 0 {
		w.Header().Set("Content-Length", strconv.FormatInt(resp.ContentLength, 10))
	}
	w.Header().Set("X-CNCacheHub-Cache", "MISS")
	w.Header().Set("X-CNCacheHub-Resource-Rule", rule.Name)
	w.WriteHeader(resp.StatusCode)
	writer := io.Writer(w)
	mw := io.MultiWriter(tmp, writer)
	n, copyErr := io.Copy(mw, resp.Body)
	if copyErr != nil {
		h.Log.Warn("resource: copy error", "err", copyErr)
		return
	}
	// 落盘成功 → rename + DB 记录
	finalPath := filepath.Join(cacheDir, digest)
	if err := os.Rename(tmpPath, finalPath); err != nil {
		h.Log.Warn("resource: rename failed", "err", err)
		return
	}
	ttl := rule.DefaultTTLSeconds
	if ttl <= 0 {
		ttl = 86400
	}
	expiresAt := time.Now().Add(time.Duration(ttl) * time.Second).Unix()
	ce := storage.ResourceCacheEntry{
		RuleID:      rule.ID,
		Path:        restPath,
		SizeBytes:   n,
		HitCount:    0,
		LastAccessAt: time.Now().Unix(),
		ExpiresAt:   expiresAt,
		ContentType: resp.Header.Get("Content-Type"),
		StoragePath: finalPath,
	}
	if _, err := h.DB.UpsertResourceCacheEntry(r.Context(), ce); err != nil {
		h.Log.Warn("resource: db upsert", "err", err)
	}
	h.Log.Info("resource: cached", "rule", rule.Name, "path", restPath, "size", n, "elapsed", time.Since(start).Milliseconds())
}

// serveFromFile 命中缓存 — 直接 ReadFile 吐给 client。
func (h *ResourceHandler) serveFromFile(w http.ResponseWriter, r *http.Request, e storage.ResourceCacheEntry, start time.Time, rule storage.ResourceRule) error {
	f, err := os.Open(e.StoragePath)
	if err != nil {
		return err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return err
	}
	w.Header().Set("Content-Type", e.ContentType)
	w.Header().Set("Content-Length", strconv.FormatInt(st.Size(), 10))
	w.Header().Set("X-CNCacheHub-Cache", "HIT")
	w.Header().Set("X-CNCacheHub-Resource-Rule", rule.Name)
	w.WriteHeader(http.StatusOK)
	_, err = io.Copy(w, f)
	h.Log.Info("resource: hit", "rule", rule.Name, "path", e.Path, "size", st.Size(), "elapsed", time.Since(start).Milliseconds())
	return err
}

// serveBypass 旁路（不缓存）。用于带敏感 query 的 URL。
func (h *ResourceHandler) serveBypass(w http.ResponseWriter, r *http.Request, ruleName, restPath, reason string) {
	rule, err := h.DB.GetResourceRuleByName(r.Context(), ruleName)
	if err != nil {
		http.Error(w, "rule not found", http.StatusNotFound)
		return
	}
	upstreamURL := strings.TrimRight(rule.UpstreamURL, "/") + "/" + restPath
	if r.URL.RawQuery != "" {
		upstreamURL += "?" + r.URL.RawQuery
	}
	req, err := http.NewRequestWithContext(r.Context(), "GET", upstreamURL, nil)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if ua := r.Header.Get("User-Agent"); ua != "" {
		req.Header.Set("User-Agent", ua)
	}
	resp, err := h.HTTP.Do(req)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()
	w.Header().Set("Content-Type", resp.Header.Get("Content-Type"))
	w.Header().Set("X-CNCacheHub-Cache", "BYPASS")
	w.Header().Set("X-CNCacheHub-Bypass", reason)
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
}

func pathDigest(p string) string {
	sum := sha256.Sum256([]byte(p))
	// 16 字节 hex 够防冲突；2^64 个不同路径才 50% 碰撞概率
	return hex.EncodeToString(sum[:16])
}

func hasSensitiveQuery(rawQuery string) bool {
	if rawQuery == "" {
		return false
	}
	v, err := url.ParseQuery(rawQuery)
	if err != nil {
		return true // 解析不出按敏感处理
	}
	sensitive := map[string]struct{}{
		"token": {}, "signature": {}, "sig": {}, "session": {},
		"auth": {}, "key": {}, "secret": {}, "password": {},
	}
	// 大小写不敏感 — url.Values.Has 是 case-sensitive，但 query key
	// 不区分大小写才是正确的安全策略（避免 TOKEN / Token / tOkEn 绕过）
	for k := range v {
		if _, ok := sensitive[strings.ToLower(k)]; ok {
			return true
		}
	}
	return false
}
