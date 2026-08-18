package cache

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const testDigest = "sha256:" + "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func newTestStore(t *testing.T, pol Policy) *FileStore {
	t.Helper()
	root := t.TempDir()
	s, err := NewFileStore(root, pol)
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	return s
}

func TestNewFileStore_CreatesRoot(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "nested", "cache")
	if _, err := NewFileStore(root, Policy{}); err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	if !fileExists(root) {
		t.Errorf("root dir not created: %s", root)
	}
}

func TestPut_Get_Roundtrip(t *testing.T) {
	s := newTestStore(t, Policy{})
	payload := []byte("hello world\n")
	r := bytes.NewReader(payload)

	reason, err := s.Put(context.Background(), "dockerhub", "library/nginx", testDigest, "text/plain", int64(len(payload)), r)
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	if reason != BypassNone {
		t.Errorf("reason = %q, want empty", reason)
	}

	// 验证文件落盘
	path, _ := s.blobPath("dockerhub", "library/nginx", testDigest)
	if !fileExists(path) {
		t.Errorf("blob file not created at %s", path)
	}

	// 验证 Get 返回内容
	rc, size, err := s.Get("dockerhub", "library/nginx", testDigest)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	defer rc.Close()
	if size != int64(len(payload)) {
		t.Errorf("size = %d, want %d", size, len(payload))
	}
	got, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !bytes.Equal(got, payload) {
		t.Errorf("payload mismatch: got %q", got)
	}
}

func TestStat(t *testing.T) {
	s := newTestStore(t, Policy{})

	hit, _, err := s.Stat("dockerhub", "library/nginx", testDigest)
	if err != nil {
		t.Fatalf("Stat (miss): %v", err)
	}
	if hit {
		t.Errorf("Stat on empty = true, want false")
	}

	payload := []byte("x")
	if _, err := s.Put(context.Background(), "dockerhub", "library/nginx", testDigest, "text/plain", 1, bytes.NewReader(payload)); err != nil {
		t.Fatalf("Put: %v", err)
	}
	hit, size, err := s.Stat("dockerhub", "library/nginx", testDigest)
	if err != nil {
		t.Fatalf("Stat (hit): %v", err)
	}
	if !hit {
		t.Errorf("Stat on present = false, want true")
	}
	if size != 1 {
		t.Errorf("size = %d, want 1", size)
	}
}

func TestDelete(t *testing.T) {
	s := newTestStore(t, Policy{})
	if _, err := s.Put(context.Background(), "dockerhub", "library/nginx", testDigest, "text/plain", 1, bytes.NewReader([]byte("x"))); err != nil {
		t.Fatalf("Put: %v", err)
	}
	if err := s.Delete("dockerhub", "library/nginx", testDigest); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	hit, _, _ := s.Stat("dockerhub", "library/nginx", testDigest)
	if hit {
		t.Errorf("still hit after Delete")
	}
	// 删第二次应幂等
	if err := s.Delete("dockerhub", "library/nginx", testDigest); err != nil {
		t.Errorf("Delete twice: %v", err)
	}
}

func TestPut_BypassSizeLimit(t *testing.T) {
	s := newTestStore(t, Policy{MaxObjectSize: 100})
	// 上游报 size=200，应旁路
	_, err := s.Put(context.Background(), "dockerhub", "library/nginx", testDigest, "text/plain", 200, bytes.NewReader([]byte("x")))
	if err == nil {
		t.Fatal("expected BypassError, got nil")
	}
	if reason, ok := IsBypass(err); !ok || reason != BypassSizeLimit {
		t.Errorf("err = %v, want BypassError size_limit", err)
	}
	// 不应该写盘
	if _, _, err := s.Get("dockerhub", "library/nginx", testDigest); !errors.Is(err, ErrNotFound) {
		t.Errorf("expected ErrNotFound after bypass, got %v", err)
	}
}

func TestPut_BypassDuringStream(t *testing.T) {
	// size=-1（未知）场景：实际读的时候超限 → 旁路
	s := newTestStore(t, Policy{MaxObjectSize: 5})
	payload := []byte("this is way too long for limit")
	_, err := s.Put(context.Background(), "dockerhub", "library/nginx", testDigest, "text/plain", -1, bytes.NewReader(payload))
	if err == nil {
		t.Fatal("expected BypassError during stream")
	}
	if reason, ok := IsBypass(err); !ok || reason != BypassSizeLimit {
		t.Errorf("err = %v, want BypassError size_limit", err)
	}
}

func TestPut_BypassDiskLow(t *testing.T) {
	// ReserveSpace 设一个天文数字 → 必旁路
	s := newTestStore(t, Policy{ReserveSpace: 1 << 50 /* 1PB */})
	_, err := s.Put(context.Background(), "dockerhub", "library/nginx", testDigest, "text/plain", 10, bytes.NewReader([]byte("xxxxxxxxxx")))
	if err == nil {
		t.Fatal("expected BypassError disk_low")
	}
	if reason, ok := IsBypass(err); !ok || reason != BypassDiskLow {
		t.Errorf("err = %v, want BypassError disk_low", err)
	}
}

func TestPut_PathTraversal(t *testing.T) {
	s := newTestStore(t, Policy{})
	cases := []struct {
		name string
		repo string
	}{
		{"dotdot", "../../etc"},
		{"leading_dotdot", "../foo"},
		{"mid_dotdot", "library/../etc"},
		{"single_dotdot", ".."},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := s.Put(context.Background(), "dockerhub", tc.repo, testDigest, "text/plain", 1, bytes.NewReader([]byte("x")))
			if err == nil {
				t.Fatal("expected error for path traversal")
			}
		})
	}
}

func TestPut_InvalidDigest(t *testing.T) {
	s := newTestStore(t, Policy{})
	bad := []string{"", "abc", "sha256:", "md5:xxx", "sha256:zzz"}
	for _, d := range bad {
		t.Run(d, func(t *testing.T) {
			_, err := s.Put(context.Background(), "dockerhub", "library/nginx", d, "text/plain", 1, bytes.NewReader([]byte("x")))
			if err == nil {
				t.Errorf("expected error for digest %q", d)
			}
		})
	}
}

func TestPut_Overwrite(t *testing.T) {
	// 重复 Put 同一 (registry, repo, digest) 应原子替换（旧 tmp 文件残留不能存在）
	s := newTestStore(t, Policy{})

	for i := 0; i < 3; i++ {
		payload := []byte(strings.Repeat("x", 100+i))
		if _, err := s.Put(context.Background(), "dockerhub", "library/nginx", testDigest, "text/plain", int64(len(payload)), bytes.NewReader(payload)); err != nil {
			t.Fatalf("Put round %d: %v", i, err)
		}
	}

	// 验证落盘后只剩一个文件，且内容是最后一次的
	// blobs 目录里只有一个文件
	blobsDir := filepath.Dir(s.blobPathForTest("dockerhub", "library/nginx", testDigest))
	blobEntries, err := os.ReadDir(blobsDir)
	if err != nil {
		t.Fatalf("ReadDir blobs: %v", err)
	}
	if len(blobEntries) != 1 {
		t.Errorf("blobs dir has %d entries, want 1: %v", len(blobEntries), blobEntries)
	}
	// 读回验证
	rc, size, err := s.Get("dockerhub", "library/nginx", testDigest)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	defer rc.Close()
	if size != 102 {
		t.Errorf("size = %d, want 102", size)
	}
	got, _ := io.ReadAll(rc)
	if len(got) != 102 {
		t.Errorf("read back length = %d, want 102", len(got))
	}
}

func TestPut_ContextCancel(t *testing.T) {
	s := newTestStore(t, Policy{})
	// 用 OpenStream + Write 验证 closed 后 Write 报错
	sw, err := s.OpenStream("dockerhub", "library/nginx", testDigest, "text/plain", -1)
	if err != nil {
		t.Fatalf("OpenStream: %v", err)
	}
	if err := sw.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	// 关闭后再 Write 应报错
	if _, err := sw.Write([]byte("x")); err == nil {
		t.Error("expected error on write after close")
	}
}

func TestPolicy_ShouldBypass(t *testing.T) {
	cases := []struct {
		name      string
		pol       Policy
		size      int64
		want      BypassReason
		wantByp   bool
	}{
		{"no policy", Policy{}, 1000, BypassNone, false},
		{"under size", Policy{MaxObjectSize: 1000}, 500, BypassNone, false},
		{"at size", Policy{MaxObjectSize: 1000}, 1000, BypassNone, false},
		{"over size", Policy{MaxObjectSize: 1000}, 1001, BypassSizeLimit, true},
		{"zero size", Policy{MaxObjectSize: 1000}, 0, BypassNone, false}, // 未知大小不预判
		{"negative size", Policy{MaxObjectSize: 1000}, -1, BypassNone, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			reason, bypass := tc.pol.ShouldBypass(tc.size)
			if bypass != tc.wantByp {
				t.Errorf("bypass = %v, want %v", bypass, tc.wantByp)
			}
			if reason != tc.want {
				t.Errorf("reason = %q, want %q", reason, tc.want)
			}
		})
	}
}

func TestValidDigest(t *testing.T) {
	cases := []struct {
		d    string
		want bool
	}{
		{"sha256:abc", true},
		{"sha512:" + strings.Repeat("a", 128), true},
		{"sha1:abcdef", true},
		{"", false},
		{"abc", false},
		{"sha256:", false},
		{"md5:xxx", false},
		{"sha256:zzz", false}, // 非 hex
		{"sha256:ab cd", false}, // 含空格
	}
	for _, tc := range cases {
		t.Run(tc.d, func(t *testing.T) {
			if got := validDigest(tc.d); got != tc.want {
				t.Errorf("validDigest(%q) = %v, want %v", tc.d, got, tc.want)
			}
		})
	}
}

func TestAvailableBytes(t *testing.T) {
	// 真实文件系统，tmp 目录应至少有 1 字节可用
	n := availableBytes(t.TempDir())
	if n < 0 {
		t.Errorf("availableBytes = %d, want >= 0", n)
	}
	if n < 1024 {
		t.Errorf("availableBytes = %d, want at least 1KB", n)
	}
}

// blobPathForTest 是 blobPath 的公开包装（仅测试用）。
func (s *FileStore) blobPathForTest(registry, repo, digest string) string {
	p, _ := s.blobPath(registry, repo, digest)
	return p
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}
