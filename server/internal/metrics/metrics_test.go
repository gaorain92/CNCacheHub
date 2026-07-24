package metrics

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cncachehub/server/internal/storage"
)

func openTestDB(t *testing.T) *storage.DB {
	t.Helper()
	dir := t.TempDir()
	db, err := storage.Open(context.Background(), filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("storage.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func TestHandler_Format(t *testing.T) {
	db := openTestDB(t)
	start := time.Now().Add(-1 * time.Hour)
	h := Handler(Source{
		DB:        db,
		Version:   "dev",
		Commit:    "test",
		StartTime: start,
		Upstreams: func() []UpstreamStatus {
			return []UpstreamStatus{
				{Name: "dockerhub", URL: "https://registry-1.docker.io", Reachable: true},
				{Name: "ghcr", URL: "https://ghcr.io", Reachable: false},
			}
		},
	})

	srv := httptest.NewServer(h)
	defer srv.Close()

	resp, err := http.Get(srv.URL)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/plain") {
		t.Errorf("content-type = %q, want text/plain", ct)
	}
	buf := make([]byte, 16384)
	n, _ := resp.Body.Read(buf)
	body := string(buf[:n])

	// 必须包含的指标
	want := []string{
		"# HELP cnch_uptime_seconds",
		"# TYPE cnch_uptime_seconds gauge",
		"cnch_uptime_seconds",
		"cnch_start_time_seconds",
		"cnch_build_info{commit=\"test\",version=\"dev\"} 1",
		"cnch_cache_entries",
		"cnch_cache_bytes",
		"cnch_request_count_24h",
		"cnch_resource_rules",
		"cnch_resource_cache_entries",
		`cnch_upstream_reachable{upstream="dockerhub",url="https://registry-1.docker.io"} 1`,
		`cnch_upstream_reachable{upstream="ghcr",url="https://ghcr.io"} 0`,
	}
	for _, w := range want {
		if !strings.Contains(body, w) {
			t.Errorf("body missing %q", w)
		}
	}
}

func TestHandler_NilDB(t *testing.T) {
	h := Handler(Source{
		DB:        nil,
		Version:   "dev",
		StartTime: time.Now(),
	})
	srv := httptest.NewServer(h)
	defer srv.Close()
	resp, _ := http.Get(srv.URL)
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
}

func TestFormatValue(t *testing.T) {
	cases := []struct {
		in   float64
		want string
	}{
		{0, "0"},
		{42, "42"},
		{1.5, "1.5"},
		{-3, "-3"},
		{1e10, "10000000000"},
	}
	for _, c := range cases {
		got := formatValue(c.in)
		if got != c.want {
			t.Errorf("formatValue(%v) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestFormatLabels(t *testing.T) {
	got := formatLabels(map[string]string{"a": "1", "b": `with"quote`})
	if !strings.Contains(got, `a="1"`) {
		t.Errorf("missing a label: %q", got)
	}
	// b 应该被 escape 为 "with\"quote"（反斜杠 + 引号）
	if !strings.Contains(got, `b="with\"quote"`) {
		t.Errorf("missing escaped b label: %q", got)
	}
}
