package proxy

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cncachehub/server/internal/cache"
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

	u, err := NewUpstream(UpstreamOptions{
		BaseURL: up.URL,
		Timeout: 10 * time.Second,
		UA:      "cncachehub-test",
	})
	if err != nil {
		t.Fatalf("NewUpstream: %v", err)
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
