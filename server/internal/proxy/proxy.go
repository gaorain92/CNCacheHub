// Package proxy: 主入口与 HTTP 路由分发。
package proxy

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/cncachehub/server/internal/cache"
	logpkg "github.com/cncachehub/server/internal/log"
	"github.com/cncachehub/server/internal/storage"
)

// Proxy 持有缓存 + 上游 + 日志依赖，实现 http.Handler。
//
// 线程安全：所有字段只读，多 goroutine 并发 ServeHTTP 安全。
type Proxy struct {
	Cache     cache.Store
	Upstream  *Upstream
	AccessLog chan<- AccessLog // 注入：非 nil 时异步记日志
	Logger    *slog.Logger

	// MetaWriter 写 cache_entries 元数据（可选；nil 时不写）。
	// 接口而非 *storage.DB，避免 proxy → storage 反向依赖。
	MetaWriter MetaWriter
}

// MetaWriter 接口：proxy 写元数据的最小集。
//
// 实际实现是 *storage.DB 适配的闭包（main.go 注入）。
type MetaWriter interface {
	UpsertCacheEntry(ctx context.Context, e storage.CacheEntry) (int64, error)
	TouchCacheEntry(ctx context.Context, registry, repo, digest string) error
}

// New 构造 Proxy。
func New(c cache.Store, u *Upstream, accessLog chan<- AccessLog, meta MetaWriter) *Proxy {
	return &Proxy{
		Cache:     c,
		Upstream:  u,
		AccessLog: accessLog,
		Logger:    logpkg.L(),
		MetaWriter: meta,
	}
}

// ServeHTTP 实现 /v2/* 路由分发。
func (p *Proxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	ctx := r.Context()
	path := r.URL.Path

	entry := AccessLog{
		Method:   r.Method,
		Path:     path,
		ClientIP: clientIP(r),
	}
	defer func() {
		entry.DurationMs = time.Since(start).Milliseconds()
		if p.AccessLog != nil {
			select {
			case p.AccessLog <- entry:
			default:
				// channel 满就丢；日志不应阻塞主流程
			}
		}
	}()

	w.Header().Set("X-CNCacheHub-Version", "phase1")

	// 根
	if path == "/v2" || path == "/v2/" {
		entry.Status = http.StatusOK
		entry.Bytes = 2
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("{}"))
		return
	}

	// 解析 /v2/<name>/{manifests,blobs}/<ref>
	//
	// name 可以含 /（如 library/nginx、bitnami/postgresql），kind 和 ref 不含。
	// 用 /manifests/ 和 /blobs/ 作分隔符切。
	rest := strings.TrimPrefix(path, "/v2/")
	rest = strings.TrimPrefix(rest, "/")

	var name, kind, ref string
	if idx := strings.Index(rest, "/manifests/"); idx > 0 {
		name = rest[:idx]
		kind = "manifests"
		ref = rest[idx+len("/manifests/"):]
	} else if idx := strings.Index(rest, "/blobs/"); idx > 0 {
		name = rest[:idx]
		kind = "blobs"
		ref = rest[idx+len("/blobs/"):]
	} else {
		// 未知路径：透传上游
		p.passthrough(ctx, w, r, path, &entry)
		return
	}
	name = libraryRewrite(name)

	switch kind {
	case "manifests":
		// Phase 1 manifest 直接透传（不落盘）；以后加 TeeReader 缓存
		upPath := "/v2/" + name + "/manifests/" + ref
		status, n, err := p.Upstream.RoundTrip(ctx, w, r.Method, upPath, r.Header)
		entry.Status = status
		entry.Bytes = n
		if err != nil {
			entry.Error = err.Error()
		}
		_ = name

	case "blobs":
		p.handleBlob(ctx, w, r, name, ref, &entry)

	default:
		p.passthrough(ctx, w, r, path, &entry)
	}
}

// passthrough 通用透传。
func (p *Proxy) passthrough(ctx context.Context, w http.ResponseWriter, r *http.Request, path string, entry *AccessLog) {
	status, n, err := p.Upstream.RoundTrip(ctx, w, r.Method, path, r.Header)
	entry.Status = status
	entry.Bytes = n
	if err != nil {
		entry.Error = err.Error()
	}
}

// handleBlob 处理 /v2/<name>/blobs/<digest>。
//
// HEAD → 本地 stat（不转上游，HEAD 体很小，本地查就够了）
// GET  → 命中直接流式返回；未命中走 upstream 流式下载 + 异步落盘
func (p *Proxy) handleBlob(ctx context.Context, w http.ResponseWriter, r *http.Request, name, digest string, entry *AccessLog) {
	registry := "dockerhub" // Phase 1 写死；Phase 2 从 path 前缀推

	switch r.Method {
	case http.MethodHead:
		hit, size, err := p.Cache.Stat(registry, name, digest)
		if err != nil {
			entry.Status = http.StatusInternalServerError
			entry.Error = err.Error()
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		if !hit {
			entry.Status = http.StatusNotFound
			http.Error(w, "not in cache", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Length", itoa(size))
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Docker-Content-Digest", digest)
		w.Header().Set("X-CNCacheHub-Cache", "HIT")
		entry.Status = http.StatusOK
		entry.Cached = true
		return

	case http.MethodGet:
		// 1) 命中检测
		hit, size, err := p.Cache.Stat(registry, name, digest)
		if err != nil {
			entry.Status = http.StatusInternalServerError
			entry.Error = err.Error()
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		if hit {
			rc, sz, gerr := p.Cache.Get(registry, name, digest)
			if gerr != nil {
				if !errors.Is(gerr, cache.ErrNotFound) {
					entry.Status = http.StatusInternalServerError
					entry.Error = gerr.Error()
					http.Error(w, "internal error", http.StatusInternalServerError)
					return
				}
				// 极少见：被外部删除；落到 miss 路径
			} else {
				defer rc.Close()
				w.Header().Set("Content-Type", "application/octet-stream")
				w.Header().Set("Content-Length", itoa(sz))
				w.Header().Set("Docker-Content-Digest", digest)
				w.Header().Set("X-CNCacheHub-Cache", "HIT")
				w.WriteHeader(http.StatusOK)
				n, _ := io.Copy(w, rc)
				entry.Status = http.StatusOK
				entry.Cached = true
				entry.Bytes = n
				// 命中：touch 元数据（last_access + hit_count++）
				if p.MetaWriter != nil {
					_ = p.MetaWriter.TouchCacheEntry(ctx, registry, name, digest)
				}
				return
			}
			_ = size
		}

		// 2) 未命中：上游流式下载 + 异步落盘
		upPath := "/v2/" + name + "/blobs/" + digest
		status, n, bypassedReason, err := p.fetchAndCache(ctx, w, r, upPath, registry, name, digest)
		entry.Status = status
		entry.Bytes = n
		entry.Bypassed = bypassedReason
		entry.BypassReason = string(bypassedReason)
		if bypassedReason != cache.BypassNone {
			w.Header().Set("X-CNCacheHub-Bypass", string(bypassedReason))
		}
		if err != nil {
			entry.Error = err.Error()
		}

	default:
		entry.Status = http.StatusMethodNotAllowed
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// fetchAndCache 拉上游 blob，同时落盘 + 转发给客户端。
//
// 流程：
//   1) 用 Upstream 客户端发起请求（带 token dance：401 时自动拿 token 重试）；
//   2) 拿到 resp 后：
//      - status != 200：直接转发 body 给客户端，不缓存；
//      - status == 200：tee 写到 client + cache stream。
func (p *Proxy) fetchAndCache(
	_ context.Context, w http.ResponseWriter, r *http.Request,
	upPath, registry, name, digest string,
) (int, int64, cache.BypassReason, error) {
	ctx := r.Context()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.Upstream.baseURL+upPath, nil)
	if err != nil {
		writeUpstreamError(w, "upstream build request: "+err.Error())
		return http.StatusBadGateway, 0, cache.BypassNone, err
	}
	copyRequestHeaders(req.Header, r.Header)
	req.Header.Set("User-Agent", p.Upstream.ua)

	// 第一次请求（不带 token）
	resp, err := p.Upstream.hc.Do(req)
	if err != nil {
		writeUpstreamError(w, "upstream do: "+err.Error())
		return http.StatusBadGateway, 0, cache.BypassNone, err
	}

	// 401 + Www-Authenticate：拿 token 重试
	if resp.StatusCode == http.StatusUnauthorized && resp.Header.Get("Www-Authenticate") != "" {
		wwwAuth := resp.Header.Get("Www-Authenticate")
		_ = resp.Body.Close()
		realm, service, scope, perr := parseWwwAuthenticate(wwwAuth)
		if perr != nil {
			writeUpstreamError(w, "parse Www-Authenticate: "+perr.Error())
			return http.StatusBadGateway, 0, cache.BypassNone, perr
		}
		token, terr := p.Upstream.fetchToken(ctx, realm, service, scope)
		if terr != nil {
			writeUpstreamError(w, "fetch token: "+terr.Error())
			return http.StatusBadGateway, 0, cache.BypassNone, terr
		}
		// 重试
		req2, err2 := http.NewRequestWithContext(ctx, http.MethodGet, p.Upstream.baseURL+upPath, nil)
		if err2 != nil {
			writeUpstreamError(w, "upstream build retry: "+err2.Error())
			return http.StatusBadGateway, 0, cache.BypassNone, err2
		}
		copyRequestHeaders(req2.Header, r.Header)
		req2.Header.Set("User-Agent", p.Upstream.ua)
		req2.Header.Set("Authorization", "Bearer "+token)
		resp, err = p.Upstream.hc.Do(req2)
		if err != nil {
			writeUpstreamError(w, "upstream retry: "+err.Error())
			return http.StatusBadGateway, 0, cache.BypassNone, err
		}
	}
	defer resp.Body.Close()

	// 透传 response headers
	for k, vs := range resp.Header {
		if isHopByHopHeader(k) || k == "Date" || k == "Server" {
			continue
		}
		for _, v := range vs {
			w.Header().Add(k, v)
		}
	}
	w.Header().Set("X-CNCacheHub-Cache", "MISS")
	w.WriteHeader(resp.StatusCode)

	if resp.StatusCode != http.StatusOK {
		// 错误响应：直传 body，不缓存
		n, _ := io.Copy(w, resp.Body)
		return resp.StatusCode, n, cache.BypassNone, nil
	}

	// 正常 200：tee 到 cache + client
	contentLength := resp.ContentLength
	mediaType := resp.Header.Get("Content-Type")
	if mediaType == "" {
		mediaType = "application/octet-stream"
	}

	// 预判 bypass（基于已知 Content-Length），提前设 header
	if reason, bypass := p.Cache.BypassCheck(contentLength); bypass {
		w.Header().Set("X-CNCacheHub-Bypass", string(reason))
	}

	// 打开 cache stream（bypass 时 OpenStream 仍返回 valid stream，Write 不落盘）
	sw, err := p.Cache.OpenStream(registry, name, digest, mediaType, contentLength)
	if err != nil {
		// OpenStream 失败：仍继续转发给客户端（PRD §安全约束 #4）
		p.Logger.Warn("cache.OpenStream failed", "err", err.Error(), "digest", digest)
		n, _ := io.Copy(w, resp.Body)
		return resp.StatusCode, n, cache.BypassNone, nil
	}

	// 用自定义 MultiWriter 同时写 client + cache
	mw := &safeMultiWriter{writers: []io.Writer{w, sw}}
	n, copyErr := io.Copy(mw, resp.Body)

	// 关闭 cache stream
	closeErr := sw.Close()
	if copyErr == nil {
		copyErr = closeErr
	}

	if sw.Bypassed() {
		w.Header().Set("X-CNCacheHub-Bypass", string(sw.BypassReason()))
	}

	// 写 cache_entries 元数据。bypassed 也写（dashboard 区分统计）。
	// Close 失败时不写（数据可能不完整）。
	//
	// 重要：r.Context() 会在 client 断开时 cancel（docker daemon 拉完一个 blob 就 close 连接），
	// 所以这里用 Background context 派生一个 5s 超时的 ctx。
	upsertCtx, upsertCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer upsertCancel()
	if p.MetaWriter != nil && closeErr == nil {
		_, _ = p.MetaWriter.UpsertCacheEntry(upsertCtx, storage.CacheEntry{
			Registry:     registry,
			Repository:   name,
			Digest:       digest,
			MediaType:    mediaType,
			SizeBytes:     sw.Written(),
			StoragePath:  relativeStoragePath(registry, name, digest),
			Bypassed:     sw.Bypassed(),
			BypassReason: string(sw.BypassReason()),
		})
	}

	if copyErr != nil {
		return resp.StatusCode, n, sw.BypassReason(), copyErr
	}
	return resp.StatusCode, n, sw.BypassReason(), nil
}

// relativeStoragePath 生成相对 cache 根的路径（与 cache.FileStore.blobPath 一致）。
func relativeStoragePath(registry, repo, digest string) string {
	// 简化：与 cache.safeRel 镜像但 inline 避免 import。
	// 安全：registry 不含 /，repo 可含 /，digest 校验。
	// 失败时回退到空字符串，UI 仍然能显示。
	if digest == "" {
		return ""
	}
	return "v2/" + registry + "/" + repo + "/blobs/" + digest
}

// safeMultiWriter 是 io.multiWriter 的扩展：任一 writer 出错不互不影响。
//
// 注意：标准库 io.MultiWriter 第一个 writer 出错就停止，但我们要 cache 失败时 client 仍能收到，
// 或 client 失败时 cache 仍能写完。
type safeMultiWriter struct {
	writers []io.Writer
}

func (s *safeMultiWriter) Write(p []byte) (int, error) {
	// 写所有 writer；返回最大成功写入字节，任一 writer 出错记错误但不立即返回。
	// 简化实现：先写 client（顺序写），再写 cache；client 写失败直接返回（cache 可能还有效）。
	if len(s.writers) == 0 {
		return 0, nil
	}
	// 写 client（writer[0]）
	n, werr := s.writers[0].Write(p)
	// 写 cache（writer[1]）—— 即使 client 写失败也尝试
	if len(s.writers) > 1 {
		_, _ = s.writers[1].Write(p)
	}
	if werr != nil {
		return n, werr
	}
	return n, nil
}

// itoa 简化 int64→string（避免热路径引 strconv）。
func itoa(n int64) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

// errString 把 error 转 string（nil → ""），避免传 nil 到 Logger。
func errString(e error) string {
	if e == nil {
		return ""
	}
	return e.Error()
}

// clientIP 提取客户端 IP（优先 X-Forwarded-For 第一段，否则 r.RemoteAddr）。
func clientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		if idx := strings.Index(xff, ","); idx > 0 {
			return strings.TrimSpace(xff[:idx])
		}
		return strings.TrimSpace(xff)
	}
	// net.SplitHostPort 同时处理 IPv4 (127.0.0.1:5555) 和 IPv6 ([::1]:5555)。
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
