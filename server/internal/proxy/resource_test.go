package proxy

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cncachehub/server/internal/cache"
	"github.com/cncachehub/server/internal/log"
	"github.com/cncachehub/server/internal/storage"
)

// newTestResourceHandler 构造一个 ResourceHandler：
//   - 真实 SQLite DB（临时目录）
//   - 真实 FileStore（临时目录）
//   - rule 默认 upstream 指向 mock server
func newTestResourceHandler(t *testing.T, upstreamURL string) (*ResourceHandler, *storage.DB, *cache.FileStore) {
	t.Helper()
	dir := t.TempDir()
	db, err := storage.Open(context.Background(), dir)
	if err != nil {
		t.Fatalf("storage.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	fs, err := cache.NewFileStore(filepath.Join(dir, "cache"), cache.Policy{MaxObjectSize: 1 << 30})
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}

	h := &ResourceHandler{
		DB:            db,
		FS:            fs,
		Log:           log.L(),
		HTTP:          &http.Client{Timeout: 5 * time.Second},
		MaxObjectSize: 1 << 30, // 1 GB
	}
	return h, db, fs
}

func seedRule(t *testing.T, db *storage.DB, name, upstreamURL, pattern string, enabled bool) storage.ResourceRule {
	t.Helper()
	r, err := db.CreateResourceRule(context.Background(), storage.ResourceRule{
		Name:              name,
		Kind:              "github_release",
		UpstreamURL:       upstreamURL,
		PathPattern:       pattern,
		DefaultTTLSeconds: 3600,
		Enabled:           enabled,
		Description:       "test rule",
	})
	if err != nil {
		t.Fatalf("CreateResourceRule: %v", err)
	}
	return r
}

// ---------------------------------------------------------------------------
// path / rule 路由
// ---------------------------------------------------------------------------

func TestResource_BadPath(t *testing.T) {
	h, db, _ := newTestResourceHandler(t, "http://up")
	seedRule(t, db, "r1", "http://up", "*", true)

	// 缺 rule name
	rr := do(t, h, http.MethodGet, "/r/")
	if rr.Code != http.StatusBadRequest {
		t.Errorf("/r/ status = %d, want 400", rr.Code)
	}

	// 缺 rest path
	rr = do(t, h, http.MethodGet, "/r/r1")
	if rr.Code != http.StatusBadRequest {
		t.Errorf("/r/r1 status = %d, want 400", rr.Code)
	}
}

func TestResource_RuleNotFound(t *testing.T) {
	h, _, _ := newTestResourceHandler(t, "http://up")
	rr := do(t, h, http.MethodGet, "/r/nonexistent/foo")
	if rr.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rr.Code)
	}
}

func TestResource_RuleDisabled(t *testing.T) {
	h, db, _ := newTestResourceHandler(t, "http://up")
	seedRule(t, db, "r1", "http://up", "*", false)

	rr := do(t, h, http.MethodGet, "/r/r1/foo")
	if rr.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403", rr.Code)
	}
}

func TestResource_PathPatternMismatch(t *testing.T) {
	h, db, _ := newTestResourceHandler(t, "http://up")
	// 只允许 *.tar.gz
	seedRule(t, db, "r1", "http://up", "*.tar.gz", true)

	rr := do(t, h, http.MethodGet, "/r/r1/secret.txt")
	if rr.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403 (path not in whitelist)", rr.Code)
	}
}

// ---------------------------------------------------------------------------
// miss → upstream → cache
// ---------------------------------------------------------------------------

func TestResource_MissThenHit(t *testing.T) {
	const payload = "release-binary-content"
	var upstreamHits atomic.Int32
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamHits.Add(1)
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Length", "22")
		w.WriteHeader(200)
		_, _ = io.WriteString(w, payload)
	}))
	t.Cleanup(up.Close)

	h, db, _ := newTestResourceHandler(t, up.URL)
	seedRule(t, db, "r1", up.URL, "*", true)

	// 1) MISS
	rr1 := do(t, h, http.MethodGet, "/r/r1/foo.tar.gz")
	if rr1.Code != 200 {
		t.Fatalf("miss status = %d, want 200", rr1.Code)
	}
	if rr1.Header().Get("X-CNCacheHub-Cache") != "MISS" {
		t.Errorf("miss header = %q, want MISS", rr1.Header().Get("X-CNCacheHub-Cache"))
	}
	if rr1.Body.String() != payload {
		t.Errorf("miss body = %q", rr1.Body.String())
	}

	// 2) 等落盘（DB UpsertResourceCacheEntry 同步）
	// 直接看 DB 是否有 entry
	deadline := time.Now().Add(1 * time.Second)
	var found bool
	for time.Now().Before(deadline) {
		rule, _ := db.GetResourceRuleByName(context.Background(), "r1")
		entry, err := db.GetResourceCacheEntry(context.Background(), rule.ID, "foo.tar.gz")
		if err == nil && entry.ID > 0 {
			found = true
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !found {
		t.Fatal("cache entry not written")
	}

	// 3) HIT — 第二次不调 upstream
	rr2 := do(t, h, http.MethodGet, "/r/r1/foo.tar.gz")
	if rr2.Code != 200 {
		t.Fatalf("hit status = %d, want 200", rr2.Code)
	}
	if rr2.Header().Get("X-CNCacheHub-Cache") != "HIT" {
		t.Errorf("hit header = %q, want HIT", rr2.Header().Get("X-CNCacheHub-Cache"))
	}
	if rr2.Body.String() != payload {
		t.Errorf("hit body = %q", rr2.Body.String())
	}
	if hits := upstreamHits.Load(); hits != 1 {
		t.Errorf("upstream called %d times, want 1 (hit should not call upstream)", hits)
	}
}

// ---------------------------------------------------------------------------
// 上游错误
// ---------------------------------------------------------------------------

func TestResource_Upstream4xx_Propagated(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(404)
		_, _ = w.Write([]byte("not found at upstream"))
	}))
	t.Cleanup(up.Close)

	h, db, _ := newTestResourceHandler(t, up.URL)
	seedRule(t, db, "r1", up.URL, "*", true)

	rr := do(t, h, http.MethodGet, "/r/r1/foo")
	if rr.Code != 404 {
		t.Errorf("status = %d, want 404", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "not found at upstream") {
		t.Errorf("error body not propagated: %q", rr.Body.String())
	}
}

func TestResource_Upstream5xx_Propagated(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(503)
	}))
	t.Cleanup(up.Close)

	h, db, _ := newTestResourceHandler(t, up.URL)
	seedRule(t, db, "r1", up.URL, "*", true)

	rr := do(t, h, http.MethodGet, "/r/r1/foo")
	if rr.Code != 503 {
		t.Errorf("status = %d, want 503", rr.Code)
	}
}

// ---------------------------------------------------------------------------
// Size bypass
// ---------------------------------------------------------------------------

func TestResource_SizeLimitBypass(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Length", "1000")
		w.WriteHeader(200)
		_, _ = w.Write(make([]byte, 1000))
	}))
	t.Cleanup(up.Close)

	h, db, _ := newTestResourceHandler(t, up.URL)
	seedRule(t, db, "r1", up.URL, "*", true)
	// 限到 100 字节
	h.MaxObjectSize = 100

	rr := do(t, h, http.MethodGet, "/r/r1/big.bin")
	if rr.Code != 200 {
		t.Fatalf("status = %d, want 200 (bypass should still forward)", rr.Code)
	}
	if got := rr.Header().Get("X-CNCacheHub-Bypass"); got != "size_limit" {
		t.Errorf("X-CNCacheHub-Bypass = %q, want size_limit", got)
	}
	if got := rr.Header().Get("X-CNCacheHub-Cache"); got == "HIT" || got == "MISS" {
		t.Errorf("bypassed should not be HIT/MISS, got %q", got)
	}
}

// ---------------------------------------------------------------------------
// Sensitive query → bypass
// ---------------------------------------------------------------------------

func TestResource_SensitiveQuery_Bypass(t *testing.T) {
	var upstreamHits atomic.Int32
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamHits.Add(1)
		w.WriteHeader(200)
		_, _ = w.Write([]byte("private-content"))
	}))
	t.Cleanup(up.Close)

	h, db, _ := newTestResourceHandler(t, up.URL)
	seedRule(t, db, "r1", up.URL, "*", true)

	rr := do(t, h, http.MethodGet, "/r/r1/foo?token=secret")
	if rr.Code != 200 {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	if got := rr.Header().Get("X-CNCacheHub-Bypass"); got != "sensitive_query" {
		t.Errorf("X-CNCacheHub-Bypass = %q, want sensitive_query", got)
	}
	if got := rr.Header().Get("X-CNCacheHub-Cache"); got != "BYPASS" {
		t.Errorf("X-CNCacheHub-Cache = %q, want BYPASS", got)
	}
	// 不应写 DB
	rule, _ := db.GetResourceRuleByName(context.Background(), "r1")
	_, err := db.GetResourceCacheEntry(context.Background(), rule.ID, "foo")
	if err == nil {
		t.Error("bypassed URL should not be cached")
	}
	// 但每次都应调 upstream（旁路不缓存）
	_ = do(t, h, http.MethodGet, "/r/r1/foo?token=secret")
	if hits := upstreamHits.Load(); hits != 2 {
		t.Errorf("upstream called %d times, want 2 (bypass should call upstream every time)", hits)
	}
}

// ---------------------------------------------------------------------------
// Path pattern 边界
// ---------------------------------------------------------------------------

func TestResource_PathPattern_Glob(t *testing.T) {
	h, db, _ := newTestResourceHandler(t, "http://up")
	// "a/*/c" pattern — 中间段 * 表示"任何单段"
	seedRule(t, db, "r1", "http://up", "a/*/c", true)

	// 匹配：a/X/c （X 任何单段）
	rr := do(t, h, http.MethodGet, "/r/r1/a/anything/c")
	if rr.Code == http.StatusForbidden {
		t.Errorf("pattern 'a/*/c' should match 'a/anything/c', got 403")
	}
	// 不匹配：段数不对
	rr2 := do(t, h, http.MethodGet, "/r/r1/a/b/c/d")
	if rr2.Code != http.StatusForbidden {
		t.Errorf("pattern 'a/*/c' should NOT match 'a/b/c/d' (4 segments), got %d", rr2.Code)
	}
	// 不匹配：精确段不对
	rr3 := do(t, h, http.MethodGet, "/r/r1/a/b/d")
	if rr3.Code != http.StatusForbidden {
		t.Errorf("pattern 'a/*/c' should NOT match 'a/b/d' (last segment != c), got %d", rr3.Code)
	}
}

func TestResource_PathPattern_DoubleStar(t *testing.T) {
	h, db, _ := newTestResourceHandler(t, "http://up")
	// ** 跨段
	seedRule(t, db, "r1", "http://up", "**/*.tar.gz", true)

	rr := do(t, h, http.MethodGet, "/r/r1/a/b/c/release.tar.gz")
	if rr.Code == http.StatusForbidden {
		t.Errorf("** pattern should match, got 403")
	}
}

// ---------------------------------------------------------------------------
// Query 透明转发
// ---------------------------------------------------------------------------

func TestResource_QueryForwarded(t *testing.T) {
	var gotQuery string
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		w.WriteHeader(200)
		_, _ = w.Write([]byte("ok"))
	}))
	t.Cleanup(up.Close)

	h, db, _ := newTestResourceHandler(t, up.URL)
	seedRule(t, db, "r1", up.URL, "*", true)

	// v=1.0 不敏感，应缓存
	_ = do(t, h, http.MethodGet, "/r/r1/foo?v=1.0")
	if gotQuery != "v=1.0" {
		t.Errorf("query not forwarded: got %q, want v=1.0", gotQuery)
	}
}

// ---------------------------------------------------------------------------
// User-Agent 透传
// ---------------------------------------------------------------------------

func TestResource_UserAgentForwarded(t *testing.T) {
	var gotUA string
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUA = r.Header.Get("User-Agent")
		w.WriteHeader(200)
	}))
	t.Cleanup(up.Close)

	h, db, _ := newTestResourceHandler(t, up.URL)
	seedRule(t, db, "r1", up.URL, "*", true)

	req := httptest.NewRequest(http.MethodGet, "/r/r1/foo", nil)
	req.Header.Set("User-Agent", "my-custom-curl/8.0")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if gotUA != "my-custom-curl/8.0" {
		t.Errorf("UA not forwarded: got %q", gotUA)
	}
}

// ---------------------------------------------------------------------------
// 上游 401 / 403 / 等 4xx 不缓存
// ---------------------------------------------------------------------------

func TestResource_4xx_NotCached(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(401)
	}))
	t.Cleanup(up.Close)

	h, db, _ := newTestResourceHandler(t, up.URL)
	seedRule(t, db, "r1", up.URL, "*", true)

	_ = do(t, h, http.MethodGet, "/r/r1/foo")
	rule, _ := db.GetResourceRuleByName(context.Background(), "r1")
	_, err := db.GetResourceCacheEntry(context.Background(), rule.ID, "foo")
	if err == nil {
		t.Error("4xx should not be cached")
	}
}

// ---------------------------------------------------------------------------
// 上游响应被完整透传
// ---------------------------------------------------------------------------

func TestResource_ResponseHeaders(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/gzip")
		w.Header().Set("ETag", `"v1"`)
		w.Header().Set("X-Custom", "from-upstream")
		w.WriteHeader(200)
		_, _ = w.Write([]byte("data"))
	}))
	t.Cleanup(up.Close)

	h, db, _ := newTestResourceHandler(t, up.URL)
	seedRule(t, db, "r1", up.URL, "*", true)

	rr := do(t, h, http.MethodGet, "/r/r1/foo")
	// Content-Type 应透传
	if rr.Header().Get("Content-Type") != "application/gzip" {
		t.Errorf("Content-Type not propagated: %q", rr.Header().Get("Content-Type"))
	}
	// 注：当前实现只透传 Content-Type（PR 后续可加更多 header 透传支持）
	// ETag / X-Custom 不传是当前设计，不是 bug
	if rr.Header().Get("X-CNCacheHub-Resource-Rule") != "r1" {
		t.Errorf("rule header not set: %q", rr.Header().Get("X-CNCacheHub-Resource-Rule"))
	}
	// Cache header 必须设
	if rr.Header().Get("X-CNCacheHub-Cache") == "" {
		t.Error("X-CNCacheHub-Cache should be set")
	}
}

// ---------------------------------------------------------------------------
// ResourceHandler 构造 + 简单健康度
// ---------------------------------------------------------------------------

func TestNewResourceHandler_NilLogger_DefaultsToSlog(t *testing.T) {
	h := NewResourceHandler(nil, nil, 1024, nil)
	if h.Log == nil {
		t.Error("Log should default to non-nil")
	}
	if h.HTTP == nil {
		t.Error("HTTP should be non-nil")
	}
	if h.HTTP.Timeout == 0 {
		t.Error("HTTP client should have a timeout")
	}
}

// ---------------------------------------------------------------------------
// 缓存文件确实写盘了
// ---------------------------------------------------------------------------

func TestResource_CacheFileOnDisk(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(200)
		_, _ = w.Write([]byte("file-content"))
	}))
	t.Cleanup(up.Close)

	h, db, fs := newTestResourceHandler(t, up.URL)
	seedRule(t, db, "r1", up.URL, "*", true)

	_ = do(t, h, http.MethodGet, "/r/r1/some/path.txt")

	// 等同步落盘
	time.Sleep(100 * time.Millisecond)

	rule, _ := db.GetResourceRuleByName(context.Background(), "r1")
	entry, err := db.GetResourceCacheEntry(context.Background(), rule.ID, "some/path.txt")
	if err != nil {
		t.Fatalf("no cache entry: %v", err)
	}
	if _, err := os.Stat(entry.StoragePath); err != nil {
		t.Errorf("cache file not on disk at %s: %v", entry.StoragePath, err)
	}
	// 缓存目录应在 fs.RootDir()/../resource/r1
	wantDirPrefix := filepath.Join(filepath.Dir(fs.RootDir()), "resource", "r1")
	if !strings.HasPrefix(entry.StoragePath, wantDirPrefix) {
		t.Errorf("storage path = %q, want prefix %q", entry.StoragePath, wantDirPrefix)
	}
}

// ---------------------------------------------------------------------------
// TTL 设置（DefaultTTLSeconds）
// ---------------------------------------------------------------------------

func TestResource_TTLApplied(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		_, _ = w.Write([]byte("ttl-test"))
	}))
	t.Cleanup(up.Close)

	h, db, _ := newTestResourceHandler(t, up.URL)
	// TTL 设为 60s
	rule := seedRule(t, db, "r1", up.URL, "*", true)
	rule.DefaultTTLSeconds = 60
	if _, err := db.UpdateResourceRule(context.Background(), rule.ID, storage.ResourceRulePatch{
		DefaultTTLSeconds: &rule.DefaultTTLSeconds,
	}); err != nil {
		t.Fatalf("UpdateResourceRule: %v", err)
	}

	before := time.Now().Unix()
	_ = do(t, h, http.MethodGet, "/r/r1/foo")
	time.Sleep(100 * time.Millisecond)

	got, err := db.GetResourceCacheEntry(context.Background(), rule.ID, "foo")
	if err != nil {
		t.Fatalf("no entry: %v", err)
	}
	// 过期时间应约 = before + 60（±2 容差）
	if got.ExpiresAt < before+58 || got.ExpiresAt > before+62 {
		t.Errorf("ExpiresAt = %d, want ~%d", got.ExpiresAt, before+60)
	}
}

// ---------------------------------------------------------------------------
// HuggingFace 模型下载 — token 注入
// ---------------------------------------------------------------------------

func TestResource_HuggingFace_TokenInjected(t *testing.T) {
	var gotAuth string
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/octet-stream")
		w.WriteHeader(200)
		_, _ = w.Write([]byte("model-weights"))
	}))
	t.Cleanup(up.Close)

	h, db, _ := newTestResourceHandler(t, up.URL)
	// 用 huggingface_models kind
	if _, err := db.CreateResourceRule(context.Background(), storage.ResourceRule{
		Name:              "hf-models-test",
		Kind:              "huggingface_models",
		UpstreamURL:       up.URL,
		PathPattern:       "*",
		DefaultTTLSeconds: 3600,
		Enabled:           true,
		Description:       "HF models",
	}); err != nil {
		t.Fatalf("CreateResourceRule: %v", err)
	}
	// 注入 token
	h.GetHuggingFaceToken = func() string { return "hf_testtoken123" }

	rr := do(t, h, http.MethodGet, "/r/hf-models-test/bert-base-uncased/resolve/main/config.json")
	if rr.Code != 200 {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	want := "Bearer hf_testtoken123"
	if gotAuth != want {
		t.Errorf("upstream Authorization = %q, want %q", gotAuth, want)
	}
}

func TestResource_HuggingFace_NoToken_NoAuth(t *testing.T) {
	var gotAuth string
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.WriteHeader(200)
	}))
	t.Cleanup(up.Close)

	h, db, _ := newTestResourceHandler(t, up.URL)
	if _, err := db.CreateResourceRule(context.Background(), storage.ResourceRule{
		Name:              "hf-models-test",
		Kind:              "huggingface_models",
		UpstreamURL:       up.URL,
		PathPattern:       "*",
		DefaultTTLSeconds: 3600,
		Enabled:           true,
	}); err != nil {
		t.Fatalf("CreateResourceRule: %v", err)
	}
	// GetHuggingFaceToken = nil（未配置） → 不注入
	rr := do(t, h, http.MethodGet, "/r/hf-models-test/foo/resolve/main/x")
	if rr.Code != 200 {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	if gotAuth != "" {
		t.Errorf("upstream Authorization should be empty when token not configured, got %q", gotAuth)
	}
}

func TestResource_HuggingFace_EmptyToken_NoAuth(t *testing.T) {
	// token 配了但空串（user 主动清空） — 不注入
	var gotAuth string
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.WriteHeader(200)
	}))
	t.Cleanup(up.Close)

	h, db, _ := newTestResourceHandler(t, up.URL)
	if _, err := db.CreateResourceRule(context.Background(), storage.ResourceRule{
		Name:              "hf-models-test",
		Kind:              "huggingface_models",
		UpstreamURL:       up.URL,
		PathPattern:       "*",
		DefaultTTLSeconds: 3600,
		Enabled:           true,
	}); err != nil {
		t.Fatalf("CreateResourceRule: %v", err)
	}
	h.GetHuggingFaceToken = func() string { return "   " } // 空白

	_ = do(t, h, http.MethodGet, "/r/hf-models-test/foo/resolve/main/x")
	if gotAuth != "" {
		t.Errorf("whitespace-only token should not inject, got Authorization = %q", gotAuth)
	}
}

func TestResource_OtherKind_TokenNotInjected(t *testing.T) {
	// 普通 rule 不应该注入 token（即使配置了 hf_token）
	var gotAuth string
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.WriteHeader(200)
	}))
	t.Cleanup(up.Close)

	h, db, _ := newTestResourceHandler(t, up.URL)
	// 普通 github kind
	seedRule(t, db, "r1", up.URL, "*", true)
	h.GetHuggingFaceToken = func() string { return "hf_should_not_inject" }

	_ = do(t, h, http.MethodGet, "/r/r1/foo/bar")
	if gotAuth != "" {
		t.Errorf("non-huggingface kind should not inject Authorization, got %q", gotAuth)
	}
}
