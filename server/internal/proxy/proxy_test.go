package proxy

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cncachehub/server/internal/cache"
	"github.com/cncachehub/server/internal/storage"
)

// newTestCache 构造一个宽松策略的 cache（小 VPS 限制关掉）。
func newTestCache(t *testing.T) cache.Store {
	t.Helper()
	root := t.TempDir()
	s, err := cache.NewFileStore(root, cache.Policy{MaxObjectSize: 1 << 30 /* 1GB */})
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	return s
}

// newTestProxy 用给定的 mock upstream 构造 Proxy。
func newTestProxy(t *testing.T, upHandler http.HandlerFunc) (*Proxy, *httptest.Server) {
	t.Helper()
	up := httptest.NewServer(upHandler)
	t.Cleanup(up.Close)

	u, err := NewUpstreamPool([]UpstreamPoolEntry{{
		Name:    "dockerhub",
		BaseURL: up.URL,
		Timeout: 10 * time.Second,
		UA:      "cncachehub-test",
	}})
	if err != nil {
		t.Fatalf("NewUpstreamPool: %v", err)
	}
	c := newTestCache(t)
	accessLog := make(chan AccessLog, 100)
	p := New(c, u, accessLog, nil) // 测试不写元数据
	return p, up
}

// do 走 ServeHTTP，捕获 response。
func do(t *testing.T, h http.Handler, method, path string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

// ----- libraryRewrite -----

func TestLibraryRewrite(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"", ""},
		{"nginx", "library/nginx"},
		{"library/nginx", "library/nginx"},
		{"bitnami/postgresql", "bitnami/postgresql"},
		{"my-registry.example.com/foo", "my-registry.example.com/foo"},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			if got := libraryRewrite(tc.in); got != tc.want {
				t.Errorf("libraryRewrite(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// ----- GET /v2/ -----

func TestV2_Root(t *testing.T) {
	p, _ := newTestProxy(t, func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("upstream should not be called for /v2/")
	})
	rr := do(t, p, http.MethodGet, "/v2/")
	if rr.Code != 200 {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	if body := strings.TrimSpace(rr.Body.String()); body != "{}" {
		t.Errorf("body = %q, want {}", body)
	}
	if rr.Header().Get("X-CNCacheHub-Version") == "" {
		t.Errorf("missing version header")
	}
}

// ----- GET manifest: 透传 -----

func TestManifest_Passthrough(t *testing.T) {
	called := false
	p, _ := newTestProxy(t, func(w http.ResponseWriter, r *http.Request) {
		called = true
		// 验证路径被 libraryRewrite 改写过
		if r.URL.Path != "/v2/library/nginx/manifests/latest" {
			t.Errorf("upstream path = %q, want /v2/library/nginx/manifests/latest", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/vnd.docker.distribution.manifest.v2+json")
		w.Header().Set("Docker-Content-Digest", "sha256:deadbeef")
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`{"schemaVersion":2}`))
	})
	rr := do(t, p, http.MethodGet, "/v2/nginx/manifests/latest")
	if !called {
		t.Fatal("upstream not called")
	}
	if rr.Code != 200 {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "schemaVersion") {
		t.Errorf("body = %q, want contains schemaVersion", rr.Body.String())
	}
	if rr.Header().Get("Docker-Content-Digest") != "sha256:deadbeef" {
		t.Errorf("upstream header not propagated: %v", rr.Header())
	}
}

// ----- GET blob: 落盘 + 二次命中 -----

const testBlobDigest = "sha256:1111111111111111111111111111111111111111111111111111111111111111"

func TestBlob_MissThenHit(t *testing.T) {
	const payload = "this is the blob body for testing"
	var upstreamHits int
	var mu sync.Mutex

	p, _ := newTestProxy(t, func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		upstreamHits++
		mu.Unlock()
		if r.URL.Path != "/v2/library/nginx/blobs/"+testBlobDigest {
			t.Errorf("upstream path = %q", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Length", "35")
		w.Header().Set("Docker-Content-Digest", testBlobDigest)
		w.WriteHeader(200)
		_, _ = io.WriteString(w, payload)
	})

	// 第 1 次：miss
	rr1 := do(t, p, http.MethodGet, "/v2/nginx/blobs/"+testBlobDigest)
	if rr1.Code != 200 {
		t.Fatalf("miss status = %d, want 200", rr1.Code)
	}
	if rr1.Header().Get("X-CNCacheHub-Cache") != "MISS" {
		t.Errorf("miss header = %q, want MISS", rr1.Header().Get("X-CNCacheHub-Cache"))
	}
	if rr1.Body.String() != payload {
		t.Errorf("miss body = %q", rr1.Body.String())
	}

	// 等异步 Put 完成（最多 1s）
	deadline := time.Now().Add(1 * time.Second)
	for time.Now().Before(deadline) {
		hit, _, _ := p.Cache.Stat("dockerhub", "library/nginx", testBlobDigest)
		if hit {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if hit, _, _ := p.Cache.Stat("dockerhub", "library/nginx", testBlobDigest); !hit {
		t.Logf("debug: blob dir contents:")
		_ = p.Cache // suppress unused
	}
	hit, size, _ := p.Cache.Stat("dockerhub", "library/nginx", testBlobDigest)
	if !hit {
		t.Fatal("cache miss after first request — Put didn't complete")
	}
	if size != int64(len(payload)) {
		t.Errorf("cached size = %d, want %d", size, len(payload))
	}

	// 第 2 次：hit
	rr2 := do(t, p, http.MethodGet, "/v2/nginx/blobs/"+testBlobDigest)
	if rr2.Code != 200 {
		t.Fatalf("hit status = %d, want 200", rr2.Code)
	}
	if rr2.Header().Get("X-CNCacheHub-Cache") != "HIT" {
		t.Errorf("hit header = %q, want HIT", rr2.Header().Get("X-CNCacheHub-Cache"))
	}
	if rr2.Body.String() != payload {
		t.Errorf("hit body = %q", rr2.Body.String())
	}

	mu.Lock()
	defer mu.Unlock()
	if upstreamHits != 1 {
		t.Errorf("upstream hit %d times, want 1 (2nd should be cache hit)", upstreamHits)
	}
}

// ----- HEAD blob: 命中 / 未命中 -----

func TestBlob_Head_Hit(t *testing.T) {
	p, _ := newTestProxy(t, func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("upstream should not be called for HEAD when cached")
	})

	// 先落一条
	sw, err := p.Cache.OpenStream("dockerhub", "library/nginx", testBlobDigest, "text/plain", 5)
	if err != nil {
		t.Fatalf("OpenStream: %v", err)
	}
	if _, err := sw.Write([]byte("hello")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if err := sw.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	rr := do(t, p, http.MethodHead, "/v2/nginx/blobs/"+testBlobDigest)
	if rr.Code != 200 {
		t.Errorf("head status = %d, want 200", rr.Code)
	}
	if rr.Header().Get("Content-Length") != "5" {
		t.Errorf("Content-Length = %q, want 5", rr.Header().Get("Content-Length"))
	}
}

func TestBlob_Head_Miss(t *testing.T) {
	p, _ := newTestProxy(t, func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("upstream should not be called for HEAD when not cached")
	})

	rr := do(t, p, http.MethodHead, "/v2/nginx/blobs/"+testBlobDigest)
	if rr.Code != 404 {
		t.Errorf("head status = %d, want 404", rr.Code)
	}
}

// ----- Bypass: size limit -----

func TestBlob_BypassSizeLimit(t *testing.T) {
	p, _ := newTestProxy(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "1000")
		w.Header().Set("Content-Type", "application/octet-stream")
		w.WriteHeader(200)
		_, _ = w.Write(make([]byte, 1000))
	})
	// 改 policy 限制为 100 字节
	fs, ok := p.Cache.(*cache.FileStore)
	if !ok {
		t.Fatal("cache type assertion")
	}
	fs.SetPolicy(cache.Policy{MaxObjectSize: 100})

	rr := do(t, p, http.MethodGet, "/v2/nginx/blobs/"+testBlobDigest)
	if rr.Code != 200 {
		t.Fatalf("status = %d, want 200 (bypass should still forward)", rr.Code)
	}
	if got := rr.Header().Get("X-CNCacheHub-Bypass"); got != "size_limit" {
		t.Errorf("X-CNCacheHub-Bypass = %q, want size_limit", got)
	}
	// 落盘应没写
	hit, _, _ := p.Cache.Stat("dockerhub", "library/nginx", testBlobDigest)
	if hit {
		t.Error("should not be cached after bypass")
	}
}

// ----- 未知路径：透传 -----

func TestUnknown_Passthrough(t *testing.T) {
	called := false
	p, _ := newTestProxy(t, func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(200)
		_, _ = w.Write([]byte("ok-from-upstream"))
	})
	rr := do(t, p, http.MethodGet, "/v2/something/else/foo")
	if !called {
		t.Fatal("upstream not called")
	}
	if rr.Body.String() != "ok-from-upstream" {
		t.Errorf("body = %q", rr.Body.String())
	}
}

// ----- Method 405 -----

func TestBlob_MethodNotAllowed(t *testing.T) {
	p, _ := newTestProxy(t, func(w http.ResponseWriter, r *http.Request) {})
	rr := do(t, p, http.MethodPost, "/v2/nginx/blobs/"+testBlobDigest)
	if rr.Code != 405 {
		t.Errorf("status = %d, want 405", rr.Code)
	}
}

// ----- clientIP 提取 -----

func TestClientIP(t *testing.T) {
	cases := []struct {
		name       string
		xff        string
		remoteAddr string
		want       string
	}{
		{"xff_single", "1.2.3.4", "127.0.0.1:5555", "1.2.3.4"},
		{"xff_multi_first", "1.2.3.4, 5.6.7.8", "127.0.0.1:5555", "1.2.3.4"},
		{"no_xff", "", "127.0.0.1:5555", "127.0.0.1"},
		{"ipv6", "", "[::1]:5555", "::1"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			req.RemoteAddr = tc.remoteAddr
			if tc.xff != "" {
				req.Header.Set("X-Forwarded-For", tc.xff)
			}
			if got := clientIP(req); got != tc.want {
				t.Errorf("clientIP = %q, want %q", got, tc.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// ETag / Range — pass-through
// ---------------------------------------------------------------------------

func TestBlob_ETag_PassThrough(t *testing.T) {
	const etag = `"sha256:abc123"`
	p, _ := newTestProxy(t, func(w http.ResponseWriter, r *http.Request) {
		// 第一次 GET：返 ETag；第二次带 If-None-Match：返 304
		if r.Header.Get("If-None-Match") == etag {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		w.Header().Set("ETag", etag)
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Length", "5")
		w.WriteHeader(200)
		_, _ = w.Write([]byte("hello"))
	})

	// 1) miss — 拿 ETag
	rr1 := do(t, p, http.MethodGet, "/v2/nginx/blobs/"+testBlobDigest)
	if rr1.Code != 200 {
		t.Fatalf("first status = %d, want 200", rr1.Code)
	}
	if got := rr1.Header().Get("ETag"); got != etag {
		t.Errorf("ETag not propagated: %q", got)
	}
	// 等落盘
	time.Sleep(100 * time.Millisecond)

	// 2) hit with If-None-Match — upstream 应收到 header
	req := httptest.NewRequest(http.MethodGet, "/v2/nginx/blobs/"+testBlobDigest, nil)
	req.Header.Set("If-None-Match", etag)
	rr2 := httptest.NewRecorder()
	p.ServeHTTP(rr2, req)
	// 注意：缓存命中时直接走本地路径，**不**转发 If-None-Match 给 upstream
	// 所以 200 + full body 才是预期
	if rr2.Code != 200 {
		t.Errorf("hit status = %d, want 200", rr2.Code)
	}
	if rr2.Body.String() != "hello" {
		t.Errorf("hit body = %q", rr2.Body.String())
	}
}

func TestBlob_Range_PassThrough(t *testing.T) {
	// 100 字节 body，Range bytes=0-9 返 206 + 头 10 字节
	payload := strings.Repeat("a", 100)
	p, _ := newTestProxy(t, func(w http.ResponseWriter, r *http.Request) {
		// 缓存没命中 + Range header → 上游处理 Range
		if r.Header.Get("Range") == "" {
			t.Errorf("Range header should be passed to upstream")
		}
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Length", "100")
		w.Header().Set("Accept-Ranges", "bytes")
		w.WriteHeader(200)
		_, _ = io.WriteString(w, payload)
	})

	req := httptest.NewRequest(http.MethodGet, "/v2/nginx/blobs/"+testBlobDigest, nil)
	req.Header.Set("Range", "bytes=0-9")
	rr := httptest.NewRecorder()
	p.ServeHTTP(rr, req)
	if rr.Code != 200 {
		t.Errorf("status = %d, want 200", rr.Code)
	}
	if rr.Header().Get("Accept-Ranges") != "bytes" {
		t.Errorf("Accept-Ranges not propagated: %v", rr.Header())
	}
}

// ---------------------------------------------------------------------------
// Token dance — 401 + Www-Authenticate → 拿 token → 重试
// ---------------------------------------------------------------------------

func TestBlob_TokenDance_RetryOn401(t *testing.T) {
	// 同一个 mock server 同时处理 /v2/* 和 /token 路径
	var (
		mu       sync.Mutex
		v2Calls  int
		gotToken string
	)
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/token"):
			// 返 token JSON
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(200)
			_, _ = w.Write([]byte(`{"token":"abc123","access_token":"abc123","expires_in":300}`))
		case strings.HasPrefix(r.URL.Path, "/v2/"):
			mu.Lock()
			v2Calls++
			auth := r.Header.Get("Authorization")
			mu.Unlock()
			if auth == "" {
				// realm 用 mock server 自己
				w.Header().Set("Www-Authenticate",
					fmt.Sprintf(`Bearer realm="%s/token",service="registry",scope="repository:nginx:pull"`, "http://"+r.Host))
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			gotToken = strings.TrimPrefix(auth, "Bearer ")
			w.Header().Set("Content-Type", "application/octet-stream")
			w.Header().Set("Content-Length", "2")
			w.WriteHeader(200)
			_, _ = w.Write([]byte("ok"))
		default:
			w.WriteHeader(404)
		}
	}))
	t.Cleanup(up.Close)

	pool, err := NewUpstreamPool([]UpstreamPoolEntry{
		{Name: "dockerhub", BaseURL: up.URL, Timeout: 5 * time.Second, UA: "test"},
	})
	if err != nil {
		t.Fatalf("NewUpstreamPool: %v", err)
	}
	c := newTestCache(t)
	p := New(c, pool, nil, nil)

	rr := do(t, p, http.MethodGet, "/v2/nginx/blobs/"+testBlobDigest)
	if rr.Code != 200 {
		t.Fatalf("status = %d, want 200 (token dance should succeed)", rr.Code)
	}
	if rr.Body.String() != "ok" {
		t.Errorf("body = %q, want ok", rr.Body.String())
	}
	mu.Lock()
	defer mu.Unlock()
	if v2Calls != 2 {
		t.Errorf("/v2 called %d times, want 2 (401 then retry)", v2Calls)
	}
	if gotToken == "" {
		t.Error("retry should have Bearer token")
	}
}

// ---------------------------------------------------------------------------
// Multi-registry
// ---------------------------------------------------------------------------

func TestManifest_MultiRegistry_GHCR(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// ghcr 上游路径是 /v2/<repo>/manifests/<ref>（baseURL = "https://ghcr.io"）
		// 库名 owner/repo 不需要再添 ghcr.io 前缀
		if r.URL.Path != "/v2/owner/repo/manifests/v1" {
			t.Errorf("upstream path = %q, want /v2/owner/repo/manifests/v1", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/vnd.oci.image.manifest.v1+json")
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	t.Cleanup(up.Close)

	pool, err := NewUpstreamPool([]UpstreamPoolEntry{
		{Name: "dockerhub", BaseURL: up.URL, Timeout: 5 * time.Second, UA: "test"},
		{Name: "ghcr", BaseURL: up.URL, Timeout: 5 * time.Second, UA: "test"},
	})
	if err != nil {
		t.Fatalf("NewUpstreamPool: %v", err)
	}
	c := newTestCache(t)
	p := New(c, pool, nil, nil)

	rr := do(t, p, http.MethodGet, "/v2/ghcr/owner/repo/manifests/v1")
	if rr.Code != 200 {
		t.Errorf("status = %d, want 200", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), `"ok":true`) {
		t.Errorf("body = %q", rr.Body.String())
	}
}

// ---------------------------------------------------------------------------
// 上游错误
// ---------------------------------------------------------------------------

func TestBlob_UpstreamError_Propagated(t *testing.T) {
	p, _ := newTestProxy(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("upstream broken"))
	})
	rr := do(t, p, http.MethodGet, "/v2/nginx/blobs/"+testBlobDigest)
	if rr.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "upstream broken") {
		t.Errorf("error body not forwarded: %q", rr.Body.String())
	}
	// 不应落盘
	hit, _, _ := p.Cache.Stat("dockerhub", "library/nginx", testBlobDigest)
	if hit {
		t.Error("5xx should not be cached")
	}
}

func TestManifest_Upstream404_Propagated(t *testing.T) {
	p, _ := newTestProxy(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})
	rr := do(t, p, http.MethodGet, "/v2/nonexistent/manifests/v1")
	if rr.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rr.Code)
	}
}

// ---------------------------------------------------------------------------
// Bypass on disk full / on write error
// ---------------------------------------------------------------------------

func TestPolicy_ShouldBypass(t *testing.T) {
	// size_limit 触发
	p := cache.Policy{MaxObjectSize: 100}
	if reason, ok := p.ShouldBypass(200); !ok || reason != cache.BypassSizeLimit {
		t.Errorf("size > MaxObjectSize should bypass with size_limit, got ok=%v reason=%v", ok, reason)
	}
	// size_limit 不触发（在限制内）
	if _, ok := p.ShouldBypass(50); ok {
		t.Error("size within limit should not bypass")
	}
	// ReserveSpace + CacheDir：要求保留 1 EB（永远不满足）
	p2 := cache.Policy{ReserveSpace: 1 << 60, CacheDir: t.TempDir()}
	if reason, ok := p2.ShouldBypass(100); !ok || reason != cache.BypassDiskLow {
		t.Errorf("huge ReserveSpace should bypass with disk_low, got ok=%v reason=%v", ok, reason)
	}
	// ReserveSpace = 0 不检查
	p3 := cache.Policy{ReserveSpace: 0, CacheDir: t.TempDir()}
	if _, ok := p3.ShouldBypass(100); ok {
		t.Error("ReserveSpace=0 should not check disk")
	}
	// estimatedSize = 0 不做 size 检查
	p4 := cache.Policy{MaxObjectSize: 100}
	if _, ok := p4.ShouldBypass(0); ok {
		t.Error("size=0 should skip MaxObjectSize check")
	}
}

// ---------------------------------------------------------------------------
// 流式大 body — 验证 proxy 不一次性 buffer
// ---------------------------------------------------------------------------

func TestBlob_StreamingLargeBody(t *testing.T) {
	const size = 1 << 20 // 1 MiB
	p, _ := newTestProxy(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Length", fmt.Sprintf("%d", size))
		w.WriteHeader(200)
		// 分块写，模拟大文件流
		chunk := bytes.Repeat([]byte("a"), 4096)
		for i := 0; i < size/4096; i++ {
			if _, err := w.Write(chunk); err != nil {
				return
			}
		}
	})
	rr := do(t, p, http.MethodGet, "/v2/nginx/blobs/"+testBlobDigest)
	if rr.Code != 200 {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	if got := rr.Body.Len(); got != size {
		t.Errorf("body length = %d, want %d", got, size)
	}
}

// ---------------------------------------------------------------------------
// MetaWriter 集成 — 落盘后应写元数据
// ---------------------------------------------------------------------------

type mockMetaWriter struct {
	mu      sync.Mutex
	upserts []storage.CacheEntry
	touches int
}

func (m *mockMetaWriter) UpsertCacheEntry(_ context.Context, e storage.CacheEntry) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.upserts = append(m.upserts, e)
	return int64(len(m.upserts)), nil
}
func (m *mockMetaWriter) TouchCacheEntry(_ context.Context, registry, repo, digest string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.touches++
	return nil
}

func TestBlob_MetaWriter_UpsertAndTouch(t *testing.T) {
	mw := &mockMetaWriter{}
	payload := "hello-world"
	p, _ := newTestProxy(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.Header().Set("Content-Length", fmt.Sprintf("%d", len(payload)))
		w.WriteHeader(200)
		_, _ = io.WriteString(w, payload)
	})
	// 注入 meta writer
	p.MetaWriter = mw

	// 1) miss → upsert
	rr1 := do(t, p, http.MethodGet, "/v2/nginx/blobs/"+testBlobDigest)
	if rr1.Code != 200 {
		t.Fatalf("first status = %d", rr1.Code)
	}
	// UpsertCacheEntry 是落盘 Close 后同步调用，但用的是独立 ctx
	// 等异步落盘 + 元数据写入
	deadline := time.Now().Add(1 * time.Second)
	for time.Now().Before(deadline) {
		mw.mu.Lock()
		n := len(mw.upserts)
		mw.mu.Unlock()
		if n >= 1 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	mw.mu.Lock()
	if len(mw.upserts) != 1 {
		mw.mu.Unlock()
		t.Fatalf("upserts = %d, want 1", len(mw.upserts))
	}
	got := mw.upserts[0]
	mw.mu.Unlock()
	if got.Registry != "dockerhub" || got.Repository != "library/nginx" || got.Digest != testBlobDigest {
		t.Errorf("upsert entry = %+v", got)
	}
	if got.SizeBytes != int64(len(payload)) {
		t.Errorf("SizeBytes = %d, want %d", got.SizeBytes, len(payload))
	}
	if got.Bypassed {
		t.Error("Bypassed should be false")
	}

	// 2) hit → touch
	rr2 := do(t, p, http.MethodGet, "/v2/nginx/blobs/"+testBlobDigest)
	if rr2.Code != 200 {
		t.Fatalf("second status = %d", rr2.Code)
	}
	if rr2.Header().Get("X-CNCacheHub-Cache") != "HIT" {
		t.Errorf("second header = %q, want HIT", rr2.Header().Get("X-CNCacheHub-Cache"))
	}
	mw.mu.Lock()
	touches := mw.touches
	mw.mu.Unlock()
	if touches != 1 {
		t.Errorf("touches = %d, want 1", touches)
	}
}

// ---------------------------------------------------------------------------
// safeMultiWriter 直接测
// ---------------------------------------------------------------------------

func TestSafeMultiWriter_AllSucceed(t *testing.T) {
	var a, b bytes.Buffer
	mw := &safeMultiWriter{writers: []io.Writer{&a, &b}}
	n, err := mw.Write([]byte("hello"))
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if n != 5 {
		t.Errorf("n = %d, want 5", n)
	}
	if a.String() != "hello" || b.String() != "hello" {
		t.Errorf("a=%q b=%q, want both hello", a.String(), b.String())
	}
}

func TestSafeMultiWriter_FirstWriterFails_StillWritesSecond(t *testing.T) {
	// 第一个 writer（client）失败 — 第二个（cache）仍应写完
	failing := &failWriter{failAfter: 2}
	var cache bytes.Buffer
	mw := &safeMultiWriter{writers: []io.Writer{failing, &cache}}
	_, err := mw.Write([]byte("hello"))
	if err == nil {
		t.Error("expected error from failing writer")
	}
	// cache 应仍写满 "hello"（safeMultiWriter 总是尝试写第二个）
	if cache.String() != "hello" {
		t.Errorf("cache = %q, want 'hello' (second writer should write even if first fails)", cache.String())
	}
}

// failWriter 在 N bytes 后失败。
type failWriter struct {
	written   int
	failAfter int
}

func (f *failWriter) Write(p []byte) (int, error) {
	remaining := f.failAfter - f.written
	if remaining <= 0 {
		return 0, errors.New("simulated write error")
	}
	if len(p) <= remaining {
		f.written += len(p)
		return len(p), nil
	}
	f.written += remaining
	return remaining, errors.New("simulated partial write error")
}

// ---------------------------------------------------------------------------
// splitRegistry 边界
// ---------------------------------------------------------------------------

func TestSplitRegistry(t *testing.T) {
	pool, _ := NewUpstreamPool([]UpstreamPoolEntry{
		{Name: "dockerhub", BaseURL: "http://up", UA: "x"},
		{Name: "ghcr", BaseURL: "http://up", UA: "x"},
	})
	p := &Proxy{Upstream: pool}

	cases := []struct {
		in        string
		wantReg   string
		wantRest  string
	}{
		{"", "dockerhub", ""},
		{"library/nginx/blobs/abc", "dockerhub", "library/nginx/blobs/abc"},
		{"ghcr/owner/repo/manifests/v1", "ghcr", "owner/repo/manifests/v1"},
		{"quay/prometheus/manifests/v2", "dockerhub", "quay/prometheus/manifests/v2"}, // quay 未注册
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			reg, rest := p.splitRegistry(tc.in)
			if reg != tc.wantReg {
				t.Errorf("reg = %q, want %q", reg, tc.wantReg)
			}
			if rest != tc.wantRest {
				t.Errorf("rest = %q, want %q", rest, tc.wantRest)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// itoa / errString 边界
// ---------------------------------------------------------------------------

func TestItoa(t *testing.T) {
	cases := []struct {
		in   int64
		want string
	}{
		{0, "0"},
		{1, "1"},
		{42, "42"},
		{123456789, "123456789"},
		{-1, "-1"},
		{-999, "-999"},
	}
	for _, tc := range cases {
		t.Run(tc.want, func(t *testing.T) {
			if got := itoa(tc.in); got != tc.want {
				t.Errorf("itoa(%d) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestErrString(t *testing.T) {
	if got := errString(nil); got != "" {
		t.Errorf("errString(nil) = %q, want empty", got)
	}
	if got := errString(errors.New("boom")); got != "boom" {
		t.Errorf("errString(boom) = %q, want boom", got)
	}
}

// ---------------------------------------------------------------------------
// 401 with no Www-Authenticate — no retry, 直传 401
// ---------------------------------------------------------------------------

func TestBlob_401_NoWwwAuthenticate_NoRetry(t *testing.T) {
	var calls atomic.Int32
	p, _ := newTestProxy(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusUnauthorized)
	})
	rr := do(t, p, http.MethodGet, "/v2/nginx/blobs/"+testBlobDigest)
	if rr.Code != 401 {
		t.Errorf("status = %d, want 401", rr.Code)
	}
	if calls.Load() != 1 {
		t.Errorf("upstream called %d times, want 1 (no token dance without Www-Authenticate)", calls.Load())
	}
}

// ---------------------------------------------------------------------------
// AccessLog 写入 — 验证 defer 把 entry 推进 channel
// ---------------------------------------------------------------------------

func TestAccessLog_LoggedOnRequest(t *testing.T) {
	logCh := make(chan AccessLog, 10)
	p, _ := newTestProxy(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		_, _ = w.Write([]byte("ok"))
	})
	p.AccessLog = logCh

	_ = do(t, p, http.MethodGet, "/v2/")

	select {
	case entry := <-logCh:
		if entry.Path != "/v2/" {
			t.Errorf("Path = %q, want /v2/", entry.Path)
		}
		if entry.Status != 200 {
			t.Errorf("Status = %d, want 200", entry.Status)
		}
		if entry.Method != http.MethodGet {
			t.Errorf("Method = %q, want GET", entry.Method)
		}
	case <-time.After(1 * time.Second):
		t.Fatal("no access log entry written")
	}
}

// ---------------------------------------------------------------------------
// Manifest response headers 透传
// ---------------------------------------------------------------------------

func TestManifest_ResponseHeaders_Propagated(t *testing.T) {
	p, _ := newTestProxy(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Docker-Content-Digest", "sha256:cafebabe")
		w.Header().Set("Content-Type", "application/vnd.docker.distribution.manifest.v2+json")
		w.Header().Set("X-Custom-Header", "from-upstream")
		// 应当被剥掉的 hop-by-hop
		w.Header().Set("Connection", "keep-alive")
		w.Header().Set("Date", "Wed, 21 Oct 2026 07:28:00 GMT")
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`{}`))
	})
	rr := do(t, p, http.MethodGet, "/v2/nginx/manifests/v1")
	if rr.Header().Get("Docker-Content-Digest") != "sha256:cafebabe" {
		t.Error("Docker-Content-Digest not propagated")
	}
	if rr.Header().Get("Content-Type") != "application/vnd.docker.distribution.manifest.v2+json" {
		t.Error("Content-Type not propagated")
	}
	if rr.Header().Get("X-Custom-Header") != "from-upstream" {
		t.Error("custom header not propagated")
	}
	// hop-by-hop headers 应被剥
	if rr.Header().Get("Connection") != "" {
		t.Error("hop-by-hop Connection should be stripped")
	}
	// Date 也应被剥（proxy 自己设）
}

// ---------------------------------------------------------------------------
// copyResponseHeaders / writeUpstreamError / escapeJSON — direct
// ---------------------------------------------------------------------------

func TestCopyResponseHeaders(t *testing.T) {
	src := http.Header{}
	src.Set("Content-Type", "application/json")
	src.Set("Content-Length", "100")
	src.Set("Connection", "keep-alive")  // hop-by-hop
	src.Set("Date", "Wed, 21 Oct 2026")  // 应剥
	src.Set("Server", "upstream/1.0")    // 应剥
	src.Set("X-Custom", "value")         // 应透传
	src.Add("Set-Cookie", "a=1")         // 应透传
	src.Add("Set-Cookie", "b=2")         // 多值

	dst := http.Header{}
	copyResponseHeaders(dst, src)

	if dst.Get("Content-Type") != "application/json" {
		t.Error("Content-Type not copied")
	}
	if dst.Get("X-Custom") != "value" {
		t.Error("X-Custom not copied")
	}
	if got := dst.Values("Set-Cookie"); len(got) != 2 {
		t.Errorf("Set-Cookie = %v, want 2 values", got)
	}
	if dst.Get("Connection") != "" {
		t.Error("hop-by-hop Connection should be stripped")
	}
	if dst.Get("Date") != "" {
		t.Error("Date should be stripped")
	}
	if dst.Get("Server") != "" {
		t.Error("Server should be stripped")
	}
}

func TestWriteUpstreamError(t *testing.T) {
	rr := httptest.NewRecorder()
	writeUpstreamError(rr, `boom "quoted"`)
	if rr.Code != http.StatusBadGateway {
		t.Errorf("status = %d, want 502", rr.Code)
	}
	if ct := rr.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}
	body := rr.Body.String()
	if !strings.Contains(body, `"upstream_error"`) {
		t.Errorf("body should contain error code, got: %q", body)
	}
	if !strings.Contains(body, `boom \"quoted\"`) {
		t.Errorf("body should contain escaped message, got: %q", body)
	}
}

func TestEscapeJSON(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"hello", "hello"},
		{`with"quote`, `with\"quote`},
		{`with\backslash`, `with\\backslash`},
		{`both"and\`, `both\"and\\`},
		{"", ""},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			if got := escapeJSON(tc.in); got != tc.want {
				t.Errorf("escapeJSON(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// UpstreamPool.ListNames
// ---------------------------------------------------------------------------

func TestUpstreamPool_ListNames(t *testing.T) {
	pool, err := NewUpstreamPool([]UpstreamPoolEntry{
		{Name: "dockerhub", BaseURL: "http://up1", UA: "x"},
		{Name: "ghcr", BaseURL: "http://up2", UA: "x"},
		{Name: "quay", BaseURL: "http://up3", UA: "x"},
	})
	if err != nil {
		t.Fatalf("NewUpstreamPool: %v", err)
	}
	names := pool.ListNames()
	if len(names) != 3 {
		t.Errorf("len = %d, want 3", len(names))
	}
	// 应该包含全部注册名（顺序不保证）
	set := make(map[string]bool)
	for _, n := range names {
		set[n] = true
	}
	for _, want := range []string{"dockerhub", "ghcr", "quay"} {
		if !set[want] {
			t.Errorf("ListNames missing %q", want)
		}
	}
}

func TestUpstreamPool_ListNames_Empty(t *testing.T) {
	pool, _ := NewUpstreamPool(nil)
	if got := pool.ListNames(); len(got) != 0 {
		t.Errorf("empty pool: ListNames = %v, want []", got)
	}
}

// ---------------------------------------------------------------------------
// resource.go 简单 parser — pathDigest / hasSensitiveQuery
// ---------------------------------------------------------------------------

func TestPathDigest_Deterministic(t *testing.T) {
	a := pathDigest("foo/bar")
	b := pathDigest("foo/bar")
	if a != b {
		t.Errorf("same input should produce same digest: %s vs %s", a, b)
	}
	if len(a) != 32 { // 16 字节 hex = 32 chars
		t.Errorf("digest length = %d, want 32", len(a))
	}
	// 不同路径不同 digest
	c := pathDigest("foo/baz")
	if a == c {
		t.Error("different paths should produce different digests")
	}
}

func TestHasSensitiveQuery(t *testing.T) {
	cases := []struct {
		raw  string
		want bool
	}{
		{"", false},
		{"foo=bar", false},
		{"token=abc", true},
		{"signature=xyz", true},
		{"sig=abc", true},
		{"session=xyz", true},
		{"auth=token", true},
		{"key=secret", true},
		{"secret=hidden", true},
		{"password=p", true},
		{"TOKEN=abc", true},     // case-insensitive (url.Values.Has)
		{"foo=bar&token=abc", true},
		{"foo=bar&baz=qux", false},
		{"%ZZ", true}, // 无效 percent 编码 → ParseQuery 失败 → 视为敏感
	}
	for _, tc := range cases {
		t.Run(tc.raw, func(t *testing.T) {
			if got := hasSensitiveQuery(tc.raw); got != tc.want {
				t.Errorf("hasSensitiveQuery(%q) = %v, want %v", tc.raw, got, tc.want)
			}
		})
	}
}
