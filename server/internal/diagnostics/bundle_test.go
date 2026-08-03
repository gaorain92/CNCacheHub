package diagnostics

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/cncachehub/server/internal/storage"
)

// ---------------------------------------------------------------------------
// httpClientWithInsecure
// ---------------------------------------------------------------------------

func TestHTTPClientWithInsecure(t *testing.T) {
	c := httpClientWithInsecure()
	if c == nil {
		t.Fatal("returned nil")
	}
	if c.Timeout != 4*time.Second {
		t.Errorf("Timeout = %v, want 4s", c.Timeout)
	}
	if c.Transport == nil {
		t.Fatal("Transport should not be nil")
	}
	// TLS 配置应允许跳过校验
	tr, ok := c.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("Transport not *http.Transport: %T", c.Transport)
	}
	if tr.TLSClientConfig == nil {
		t.Fatal("TLSClientConfig should not be nil")
	}
	if !tr.TLSClientConfig.InsecureSkipVerify {
		t.Error("InsecureSkipVerify should be true")
	}
	// 主 client 的 Transport 仍应安全（不能误共享）
	if httpClient.Transport == tr {
		t.Error("httpClientWithInsecure should return independent transport, not share with global")
	}
}

// ---------------------------------------------------------------------------
// csvSafe
// ---------------------------------------------------------------------------

func TestCSVSafe_Plain(t *testing.T) {
	if got := csvSafe("hello"); got != "hello" {
		t.Errorf("got %q, want hello", got)
	}
}

func TestCSVSafe_WithComma(t *testing.T) {
	if got := csvSafe("a,b,c"); got != `"a,b,c"` {
		t.Errorf("got %q, want quoted", got)
	}
}

func TestCSVSafe_WithQuote(t *testing.T) {
	if got := csvSafe(`she said "hi"`); got != `"she said ""hi"""` {
		t.Errorf("got %q, want escaped", got)
	}
}

func TestCSVSafe_WithNewline(t *testing.T) {
	if got := csvSafe("line1\nline2"); got != "line1\nline2" {
		// 验证 quoting
		if !strings.HasPrefix(got, `"`) || !strings.HasSuffix(got, `"`) {
			t.Errorf("newline-containing should be quoted, got %q", got)
		}
	}
}

func TestCSVSafe_Empty(t *testing.T) {
	if got := csvSafe(""); got != "" {
		t.Errorf("empty string should be unchanged, got %q", got)
	}
}

// ---------------------------------------------------------------------------
// WriteBundle / BundleHandler
// ---------------------------------------------------------------------------

// newTestBundleSource 构造一个最小可用的 BundleSource。
// 需要 admin user（多数 SQL 有 FK 约束）。
func newTestBundleSource(t *testing.T) BundleSource {
	t.Helper()
	dir := t.TempDir()
	db, err := storage.Open(context.Background(), dir)
	if err != nil {
		t.Fatalf("storage.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	_, err = db.CreateUser(context.Background(), "tester", "x", true)
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	return BundleSource{
		DB:           db,
		Version:      "v0.1.0-test",
		Commit:       "abc1234",
		StartTime:    time.Now().Add(-1 * time.Hour),
		HTTPAddr:     ":8082",
		CacheDir:     "/tmp/cache",
		DataDir:      dir,
		UpstreamURL:  "https://registry-1.docker.io",
		MaxObjectMB:  500,
		ReserveGB:    1,
		CacheTotalGB: 10,
		LogPath:      "",
	}
}

// extractTarGz 把 gzip+tar 内容解开，返所有文件名 + 内容 map。
func extractTarGz(t *testing.T, b []byte) map[string][]byte {
	t.Helper()
	gz, err := gzip.NewReader(bytes.NewReader(b))
	if err != nil {
		t.Fatalf("gzip.NewReader: %v", err)
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	files := make(map[string][]byte)
	for {
		hdr, err := tr.Next()
		if err != nil {
			break
		}
		if hdr.Typeflag != tar.TypeReg {
			continue
		}
		var buf bytes.Buffer
		if _, err := buf.ReadFrom(tr); err != nil {
			t.Fatalf("read %s: %v", hdr.Name, err)
		}
		files[hdr.Name] = buf.Bytes()
	}
	return files
}

func TestWriteBundle_IncludesExpectedFiles(t *testing.T) {
	src := newTestBundleSource(t)
	var buf bytes.Buffer
	if err := WriteBundle(context.Background(), &buf, src); err != nil {
		t.Fatalf("WriteBundle: %v", err)
	}
	files := extractTarGz(t, buf.Bytes())

	// 必备文件（不依赖 DB 的部分）
	required := []string{
		"README.txt",
		"system.json",
		"config.json",
		"cache_policy.json",
		"settings_extra.json",
	}
	for _, name := range required {
		if _, ok := files[name]; !ok {
			t.Errorf("missing required file %q in bundle", name)
		}
	}

	// system.json 应包含 version + commit
	if s, ok := files["system.json"]; ok {
		if !strings.Contains(string(s), "v0.1.0-test") {
			t.Errorf("system.json should contain version, got: %s", s)
		}
		if !strings.Contains(string(s), "abc1234") {
			t.Errorf("system.json should contain commit, got: %s", s)
		}
		if !strings.Contains(string(s), "uptime") {
			t.Errorf("system.json should contain uptime, got: %s", s)
		}
	}

	// config.json 应包含 http_addr
	if c, ok := files["config.json"]; ok {
		if !strings.Contains(string(c), ":8082") {
			t.Errorf("config.json should contain http_addr, got: %s", c)
		}
	}
}

func TestWriteBundle_WithDB_IncludesTableFiles(t *testing.T) {
	src := newTestBundleSource(t)
	var buf bytes.Buffer
	if err := WriteBundle(context.Background(), &buf, src); err != nil {
		t.Fatalf("WriteBundle: %v", err)
	}
	files := extractTarGz(t, buf.Bytes())

	// 至少要有一个从 DB 来的文件
	dbFiles := []string{"settings.json", "rules.json", "preheat_tasks.json", "summary.json"}
	found := false
	for _, name := range dbFiles {
		if _, ok := files[name]; ok {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("no DB-derived file in bundle (looked for: %v)", dbFiles)
	}
}

func TestWriteBundle_HandlesDBErrors(t *testing.T) {
	// nil DB 时 WriteBundle 应 best-effort：error 一律吞，返回 nil
	src := BundleSource{
		DB:          nil,
		Version:     "v0.1.0",
		HTTPAddr:    ":8082",
		CacheDir:    "/tmp/cache",
		DataDir:     "/tmp/data",
		UpstreamURL: "https://example.com",
		StartTime:   time.Now(),
	}
	var buf bytes.Buffer
	err := WriteBundle(context.Background(), &buf, src)
	if err != nil {
		t.Errorf("WriteBundle with nil DB should be best-effort, got error: %v", err)
	}
	// system.json 应该还是有
	files := extractTarGz(t, buf.Bytes())
	if _, ok := files["system.json"]; !ok {
		t.Error("system.json should be in bundle even with nil DB")
	}
}

func TestBundleHandler_HTTP(t *testing.T) {
	src := newTestBundleSource(t)
	handler := BundleHandler(src)

	req := httptest.NewRequest(http.MethodPost, "/api/diagnostics/bundle", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rr.Code)
	}
	// Content-Type
	if ct := rr.Header().Get("Content-Type"); ct != "application/gzip" {
		t.Errorf("Content-Type = %q, want application/gzip", ct)
	}
	// Content-Disposition: attachment + filename
	cd := rr.Header().Get("Content-Disposition")
	if !strings.HasPrefix(cd, `attachment; filename="cncachehub-diagnostics-`) {
		t.Errorf("Content-Disposition = %q, want attachment + filename", cd)
	}
	if !strings.HasSuffix(cd, `.tar.gz"`) {
		t.Errorf("Content-Disposition should end with .tar.gz, got: %q", cd)
	}

	// body 应是可解压的 tar.gz
	body := rr.Body.Bytes()
	if len(body) == 0 {
		t.Fatal("empty body")
	}
	files := extractTarGz(t, body)
	if len(files) == 0 {
		t.Error("no files in bundle")
	}
}
