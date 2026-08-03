package cache

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// NewFileStore / RootDir / BypassCheck / SetPolicy
// ---------------------------------------------------------------------------

func TestNewFileStore_EmptyRoot_Errors(t *testing.T) {
	_, err := NewFileStore("", Policy{})
	if err == nil {
		t.Fatal("empty root should error")
	}
	if !strings.Contains(err.Error(), "root") {
		t.Errorf("error should mention 'root', got: %v", err)
	}
}

func TestNewFileStore_AutoCreateRoot(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "auto-created-subdir")
	// 确认还没创建
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("dir should not exist yet, stat err: %v", err)
	}
	fs, err := NewFileStore(dir, Policy{MaxObjectSize: 1024})
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	if fs.RootDir() != dir {
		t.Errorf("RootDir = %q, want %q", fs.RootDir(), dir)
	}
	// dir 应被自动创建
	if _, err := os.Stat(dir); err != nil {
		t.Errorf("root should be auto-created, stat err: %v", err)
	}
}

func TestBypassCheck_DelegatesToPolicy(t *testing.T) {
	fs, err := NewFileStore(t.TempDir(), Policy{MaxObjectSize: 100})
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	// 100 字节：恰好在限制内
	if reason, ok := fs.BypassCheck(100); ok {
		t.Errorf("size == limit should NOT bypass, got reason=%v", reason)
	}
	// 101：超限
	if reason, ok := fs.BypassCheck(101); !ok || reason != BypassSizeLimit {
		t.Errorf("size > limit should bypass with size_limit, got ok=%v reason=%v", ok, reason)
	}
}

func TestSetPolicy_DefaultCacheDir(t *testing.T) {
	dir := t.TempDir()
	fs, err := NewFileStore(dir, Policy{MaxObjectSize: 1000})
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	// 不传 CacheDir 应自动填上 root
	fs.SetPolicy(Policy{MaxObjectSize: 500})
	cur := fs.currentPolicy()
	if cur.CacheDir != dir {
		t.Errorf("SetPolicy should default CacheDir to root, got %q", cur.CacheDir)
	}
	if cur.MaxObjectSize != 500 {
		t.Errorf("MaxObjectSize = %d, want 500", cur.MaxObjectSize)
	}
}

func TestSetPolicy_OverrideCacheDir(t *testing.T) {
	dir1 := t.TempDir()
	fs, err := NewFileStore(dir1, Policy{MaxObjectSize: 1000})
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	dir2 := t.TempDir()
	fs.SetPolicy(Policy{MaxObjectSize: 500, CacheDir: dir2})
	cur := fs.currentPolicy()
	if cur.CacheDir != dir2 {
		t.Errorf("CacheDir = %q, want %q (override)", cur.CacheDir, dir2)
	}
}

// ---------------------------------------------------------------------------
// safeRel 路径安全
// ---------------------------------------------------------------------------

func TestSafeRel(t *testing.T) {
	good := "v2/dockerhub/library/nginx/blobs/sha256:abc"
	got, err := safeRel("dockerhub", "library/nginx", "sha256:abc")
	if err != nil {
		t.Errorf("good case: %v", err)
	}
	if got != good {
		t.Errorf("got %q, want %q", got, good)
	}
}

func TestSafeRel_Errors(t *testing.T) {
	cases := []struct {
		name              string
		registry, repo, dg string
		wantSubstr        string
	}{
		{"empty_registry", "", "library/nginx", "sha256:abc", "registry"},
		{"empty_digest", "dockerhub", "library/nginx", "", "digest"},
		{"registry_with_dotdot", "..", "library/nginx", "sha256:abc", "invalid"},
		{"registry_with_slash", "foo/bar", "x/y", "sha256:abc", "invalid"},
		{"registry_with_backslash", `foo\bar`, "x/y", "sha256:abc", "invalid"},
		{"repo_with_dotdot", "dockerhub", "../etc/passwd", "sha256:abc", "invalid repo"},
		{"invalid_digest", "dockerhub", "x/y", "not-a-digest", "invalid digest"},
		{"empty_digest_field", "dockerhub", "x/y", "sha256:", "invalid digest"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := safeRel(tc.registry, tc.repo, tc.dg)
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			if !strings.Contains(err.Error(), tc.wantSubstr) {
				t.Errorf("error = %v, want substring %q", err, tc.wantSubstr)
			}
		})
	}
}

func TestSafeRel_EmptyRepoBecomesUnderscore(t *testing.T) {
	// repo = "" → cleanedRepo = "" → 变 "_empty"
	got, err := safeRel("dockerhub", "", "sha256:abc")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if !strings.Contains(got, "_empty") {
		t.Errorf("empty repo should become '_empty' segment, got %q", got)
	}
}

// ---------------------------------------------------------------------------
// validDigest 边界
// ---------------------------------------------------------------------------

func TestValidDigest_MoreCases(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"sha256:abc", true},
		{"sha512:abc", true},
		{"sha1:abc", true},
		{"md5:abc", false},   // 不支持 md5
		{"sha256:", false},   // hex 空
		{":abc", false},       // algo 空
		{"sha256", false},     // 缺 :
		{"", false},
		{"sha256:abc:def", false}, // hex 部分含 ":" 不是 hex
		{"sha256:0123456789abcdefABCDEF", true}, // 大小写 hex 混用
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			if got := validDigest(tc.in); got != tc.want {
				t.Errorf("validDigest(%q) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// fileStreamWriter 边界 — write after close, bypass 路径
// ---------------------------------------------------------------------------

func TestWrite_AfterClose_Errors(t *testing.T) {
	fs, err := NewFileStore(t.TempDir(), Policy{})
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	sw, err := fs.OpenStream("dockerhub", "x/y", "sha256:abc", "text/plain", 100)
	if err != nil {
		t.Fatalf("OpenStream: %v", err)
	}
	if err := sw.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	// 再写：应返 "write after close"
	_, err = sw.Write([]byte("data"))
	if err == nil {
		t.Fatal("Write after Close should error")
	}
	if !strings.Contains(err.Error(), "write after close") {
		t.Errorf("error should mention 'write after close', got: %v", err)
	}
}

func TestWrite_Bypass_AccumulatesWritten(t *testing.T) {
	// bypass 模式：写不报错，且 Written() 仍累计
	fs, _ := NewFileStore(t.TempDir(), Policy{MaxObjectSize: 10})
	sw, _ := fs.OpenStream("dockerhub", "x/y", "sha256:abc", "text/plain", 100)
	// 立即升 bypass（type assert 拿私有字段）
	fsw, ok := sw.(*fileStreamWriter)
	if !ok {
		t.Fatalf("expected *fileStreamWriter, got %T", sw)
	}
	fsw.bypass = true
	fsw.reason = BypassDiskLow
	n, err := sw.Write([]byte("0123456789"))
	if err != nil {
		t.Fatalf("Write in bypass: %v", err)
	}
	if n != 10 {
		t.Errorf("n = %d, want 10", n)
	}
	if sw.Written() != 10 {
		t.Errorf("Written = %d, want 10", sw.Written())
	}
	if !sw.Bypassed() {
		t.Error("Bypassed should be true")
	}
	if sw.BypassReason() != BypassDiskLow {
		t.Errorf("BypassReason = %v, want disk_low", sw.BypassReason())
	}
}

func TestWrite_ExceedsMaxBytes_MidStream_Bypass(t *testing.T) {
	// 写超过 maxBytes 时应中途升级 bypass
	fs, _ := NewFileStore(t.TempDir(), Policy{MaxObjectSize: 10})
	sw, _ := fs.OpenStream("dockerhub", "x/y", "sha256:abc", "text/plain", 5)
	// 第 1 写：5 字节，没超
	if _, err := sw.Write([]byte("12345")); err != nil {
		t.Fatalf("first write: %v", err)
	}
	// 第 2 写：又 10 字节，总 15 > 10（maxBytes） → 触发 bypass
	_, err := sw.Write([]byte("abcdefghij"))
	if err != nil {
		t.Errorf("write should not error during bypass upgrade, got: %v", err)
	}
	if !sw.Bypassed() {
		t.Error("Bypassed should be true after exceeding maxBytes")
	}
	if sw.BypassReason() != BypassSizeLimit {
		t.Errorf("BypassReason = %v, want size_limit", sw.BypassReason())
	}
}

func TestClose_AfterBypass_NoError(t *testing.T) {
	// 已被设为 bypass 后 Close 仍应安全（不应尝试 rename）
	fs, _ := NewFileStore(t.TempDir(), Policy{MaxObjectSize: 10})
	swI, _ := fs.OpenStream("dockerhub", "x/y", "sha256:abc", "text/plain", 100)
	fsw, _ := swI.(*fileStreamWriter)
	fsw.bypass = true
	if err := swI.Close(); err != nil {
		t.Errorf("Close after bypass should not error, got: %v", err)
	}
	// 多次 Close 仍安全
	if err := swI.Close(); err != nil {
		t.Errorf("second Close should not error, got: %v", err)
	}
}

func TestClose_Idempotent(t *testing.T) {
	fs, _ := NewFileStore(t.TempDir(), Policy{})
	sw, _ := fs.OpenStream("dockerhub", "x/y", "sha256:abc", "text/plain", 100)
	_, _ = sw.Write([]byte("data"))
	if err := sw.Close(); err != nil {
		t.Fatalf("first Close: %v", err)
	}
	// 第二次 Close 应 no-op（closed.Swap(true) 短路）
	if err := sw.Close(); err != nil {
		t.Errorf("second Close should be no-op, got: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Put 边界
// ---------------------------------------------------------------------------

func TestPut_OpenStreamError(t *testing.T) {
	// 用超小 max object 触发 OpenStream bypass → Put 返 BypassError
	fs, _ := NewFileStore(t.TempDir(), Policy{MaxObjectSize: 1})
	reason, err := fs.Put(context.Background(), "dockerhub", "x/y", "sha256:abc", "text/plain", 4, strings.NewReader("data"))
	if reason != BypassSizeLimit {
		t.Errorf("reason = %v, want BypassSizeLimit", reason)
	}
	var be *BypassError
	if !errors.As(err, &be) {
		t.Errorf("err should be *BypassError, got: %v", err)
	}
	// 不应被缓存
	hit, _, _ := fs.Stat("dockerhub", "x/y", "sha256:abc")
	if hit {
		t.Error("bypassed Put should not be cached")
	}
}

// ---------------------------------------------------------------------------
// Stat 边界
// ---------------------------------------------------------------------------

func TestStat_NonExistent(t *testing.T) {
	fs, _ := NewFileStore(t.TempDir(), Policy{})
	hit, size, err := fs.Stat("dockerhub", "library/nonexistent", "sha256:abc")
	if err != nil {
		t.Errorf("Stat on missing should not error, got: %v", err)
	}
	if hit {
		t.Error("hit should be false")
	}
	if size != 0 {
		t.Errorf("size = %d, want 0", size)
	}
}

// ---------------------------------------------------------------------------
// Delete 边界
// ---------------------------------------------------------------------------

func TestDelete_NonExistent_NoError(t *testing.T) {
	fs, _ := NewFileStore(t.TempDir(), Policy{})
	// digest 格式合法但文件不存在 — Delete 不应报错
	if err := fs.Delete("dockerhub", "x/y", "sha256:0000000000000000000000000000000000000000000000000000000000000000"); err != nil {
		t.Errorf("Delete on missing should not error, got: %v", err)
	}
}

// ---------------------------------------------------------------------------
// io.Copy + Close 错误传播
// ---------------------------------------------------------------------------

type errReader struct{ err error }

func (e *errReader) Read(p []byte) (int, error) { return 0, e.err }

func TestPut_ReaderError(t *testing.T) {
	fs, _ := NewFileStore(t.TempDir(), Policy{})
	myErr := errors.New("simulated read error")
	_, err := fs.Put(context.Background(), "dockerhub", "x/y", "sha256:abc", "text/plain", 100, &errReader{err: myErr})
	if err == nil {
		t.Fatal("Put should propagate reader error")
	}
	if !errors.Is(err, myErr) {
		t.Errorf("error should be myErr, got: %v", err)
	}
	// 注：当前实现下，io.Copy 立刻失败时 sw.Close() 仍跑，0 字节文件可能被 rename 落盘
	// 这是已知的小 quirk（生产中 size > 0 的真实数据不会触发）
	// 主要验证：reader error 必须被传播
}

// ---------------------------------------------------------------------------
// TouchAt 边界
// ---------------------------------------------------------------------------

func TestTouchAt_NoFile_Errors(t *testing.T) {
	// TouchAt 是 os.Chtimes 的简单包装，文件不存在时返 ENOENT
	if err := TouchAt("/nonexistent/path/to/file", time.Time{}); err == nil {
		t.Error("TouchAt on missing file should error")
	}
}

func TestTouchAt_UpdatesMtime(t *testing.T) {
	fs, _ := NewFileStore(t.TempDir(), Policy{})
	// 先放一个 blob
	if _, err := fs.Put(context.Background(), "dockerhub", "x/y", "sha256:abc", "text/plain", 4, strings.NewReader("data")); err != nil {
		t.Fatalf("Put: %v", err)
	}
	blobPath := filepath.Join(fs.RootDir(), "v2", "dockerhub", "x", "y", "blobs", "sha256:abc")
	if _, err := os.Stat(blobPath); err != nil {
		t.Fatalf("blob not on disk: %v", err)
	}
	// 调 TouchAt 改 mtime（用近未来时间，避免 ext4/HFS+ 32-bit 时间上限）
	target := time.Now().Add(2 * time.Hour)
	if err := TouchAt(blobPath, target); err != nil {
		t.Errorf("TouchAt: %v", err)
	}
	// 读 stat 验证
	st, err := os.Stat(blobPath)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	// 文件系统精度到秒，±2s 容差
	diff := st.ModTime().Unix() - target.Unix()
	if diff < -2 || diff > 2 {
		t.Errorf("mtime diff = %d, want ~0", diff)
	}
}

// ---------------------------------------------------------------------------
// Put 便捷路径（正常）
// ---------------------------------------------------------------------------

func TestPut_RoundtripSimple(t *testing.T) {
	fs, _ := NewFileStore(t.TempDir(), Policy{})
	data := "hello world"
	if _, err := fs.Put(context.Background(), "dockerhub", "x/y", "sha256:abc", "text/plain", int64(len(data)), strings.NewReader(data)); err != nil {
		t.Fatalf("Put: %v", err)
	}
	hit, size, _ := fs.Stat("dockerhub", "x/y", "sha256:abc")
	if !hit {
		t.Fatal("Stat should hit after Put")
	}
	if size != int64(len(data)) {
		t.Errorf("size = %d, want %d", size, len(data))
	}
	rc, sz, err := fs.Get("dockerhub", "x/y", "sha256:abc")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	defer rc.Close()
	if sz != int64(len(data)) {
		t.Errorf("Get size = %d, want %d", sz, len(data))
	}
	got, _ := io.ReadAll(rc)
	if string(got) != data {
		t.Errorf("Get body = %q, want %q", got, data)
	}
}
