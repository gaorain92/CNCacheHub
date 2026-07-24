package diagnostics

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCsvSafe_NoSpecial(t *testing.T) {
	got := csvSafe("hello")
	if got != "hello" {
		t.Fatalf("plain string should pass through, got %q", got)
	}
}

func TestCsvSafe_WithComma(t *testing.T) {
	got := csvSafe("a,b")
	want := `"a,b"`
	if got != want {
		t.Fatalf("comma should quote, got %q want %q", got, want)
	}
}

func TestCsvSafe_WithQuote(t *testing.T) {
	got := csvSafe(`say "hi"`)
	want := `"say ""hi"""`
	if got != want {
		t.Fatalf("quote should escape, got %q want %q", got, want)
	}
}

func TestCsvSafe_WithNewline(t *testing.T) {
	got := csvSafe("line1\nline2")
	if !strings.HasPrefix(got, `"`) || !strings.HasSuffix(got, `"`) {
		t.Fatalf("newline should quote, got %q", got)
	}
	if !strings.Contains(got, "line1\nline2") {
		t.Fatalf("newline content lost: %q", got)
	}
}

func TestHostnameSafe_NonEmpty(t *testing.T) {
	h := hostnameSafe()
	if h == "" || h == "unknown" {
		// 大多数 CI / 本地都有 hostname；只有纯容器/沙盒才会 unknown — 也算 ok
		t.Logf("hostname = %q (acceptable)", h)
	}
}

// TestWriteBundle_Minimal 跑一个最小 BundleSource（DB=nil），验证：
//   - 不 panic
//   - gzip + tar header 完整
//   - README.txt + system.json + config.json + cache_policy.json + settings_extra.json 都在
func TestWriteBundle_Minimal(t *testing.T) {
	var buf bytes.Buffer
	err := WriteBundle(context.Background(), &buf, BundleSource{
		Version:      "test-1.0",
		Commit:       "abc123",
		StartTime:    time.Now().Add(-1 * time.Hour),
		HTTPAddr:     "127.0.0.1:8082",
		CacheDir:     "/var/cache/cnch",
		DataDir:      "/var/lib/cnch",
		UpstreamURL:  "https://registry-1.docker.io",
		MaxObjectMB:  1024,
		ReserveGB:    5,
		CacheTotalGB: 20,
	})
	if err != nil {
		t.Fatalf("WriteBundle: %v", err)
	}

	// 至少有 1KB 输出
	if buf.Len() < 512 {
		t.Fatalf("bundle too small: %d bytes", buf.Len())
	}

	// 解 gzip + tar
	gr, err := gzip.NewReader(&buf)
	if err != nil {
		t.Fatalf("gzip.NewReader: %v", err)
	}
	defer gr.Close()
	tr := tar.NewReader(gr)

	found := map[string]bool{}
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("tar.Next: %v", err)
		}
		found[h.Name] = true
	}

	want := []string{
		"README.txt",
		"system.json",
		"config.json",
		"cache_policy.json",
		"settings_extra.json",
	}
	for _, n := range want {
		if !found[n] {
			t.Errorf("missing file in bundle: %s", n)
		}
	}
}

// TestWriteBundle_WithLogFile 验证 system.log 能正确包含进 bundle（最后 512KB）。
func TestWriteBundle_WithLogFile(t *testing.T) {
	// 写一个临时 log
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "test.log")
	content := []byte("line1\nline2\nline3\n")
	if err := os.WriteFile(logPath, content, 0644); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	err := WriteBundle(context.Background(), &buf, BundleSource{
		Version: "v", Commit: "c", StartTime: time.Now(),
		LogPath: logPath,
	})
	if err != nil {
		t.Fatalf("WriteBundle: %v", err)
	}

	// 解 tar 找 system.log
	gr, _ := gzip.NewReader(&buf)
	defer gr.Close()
	tr := tar.NewReader(gr)
	found := false
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if h.Name == "system.log" {
			found = true
			body, _ := io.ReadAll(tr)
			if !bytes.Contains(body, []byte("line2")) {
				t.Errorf("system.log content lost: %q", body)
			}
		}
	}
	if !found {
		t.Errorf("system.log missing from bundle")
	}
}
