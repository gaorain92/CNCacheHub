// Package cache 实现 blob 落盘缓存。
//
// 路径布局（content-addressable + 按 registry 分桶）：
//
//	${CacheDir}/v2/${registry}/${repo}/blobs/${digest}
//
// 设计原则：
//   - 流式：Get / OpenStream 都用 io.Copy，绝不 io.ReadAll；
//   - 原子写：Close 时走 tmp + rename，断电不残留半成品；
//   - Bypass 旁路：size 超限 或 磁盘空间不足 → 不缓存但仍允许调用方决定是否转发；
//   - 不做驱逐：清理留给 Phase 1.2 cron 任务；
//   - 不做加密：blob 是公开数据，无需额外处理。
//
// 未来扩展：
//   - Phase 2 加 SteamCMD：新增 ${CacheDir}/steam/... 同样接口；
//   - Phase 2 加 LRU 清理：增 cleanup 方法，文件按 last_access_at 排序删。
package cache

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/sys/unix"
)

// BypassReason 表示绕过缓存的原因（响应头 X-CNCacheHub-Bypass 用）。
type BypassReason string

const (
	// BypassNone 不旁路，正常缓存。
	BypassNone BypassReason = ""
	// BypassSizeLimit 单对象超过 max_object_size 上限。
	BypassSizeLimit BypassReason = "size_limit"
	// BypassDiskLow 缓存目录所在文件系统可用空间低于 reserve_space。
	BypassDiskLow BypassReason = "disk_low"
)

// BypassError 表示落盘被旁路。
// 业务层捕获后应仍继续转发（PRD §安全约束 #4）。
type BypassError struct {
	Reason BypassReason
}

func (e *BypassError) Error() string {
	return "cache bypass: " + string(e.Reason)
}

// IsBypass 判断 err 是否为 BypassError。
func IsBypass(err error) (BypassReason, bool) {
	if err == nil {
		return BypassNone, false
	}
	var be *BypassError
	if errors.As(err, &be) {
		return be.Reason, true
	}
	return BypassNone, false
}

// ErrNotFound 缓存未命中。
var ErrNotFound = errors.New("cache: not found")

// Policy 决定何时旁路。
type Policy struct {
	// MaxObjectSize 单对象最大字节数；0 = 不限。
	MaxObjectSize int64
	// ReserveSpace 缓存目录所在文件系统最少保留字节数；0 = 不检查。
	ReserveSpace int64
	// CacheDir 用于 statfs；其它检查不需要这个字段。
	CacheDir string
}

// ShouldBypass 估算一个对象是否应该旁路。
func (p Policy) ShouldBypass(estimatedSize int64) (BypassReason, bool) {
	if p.MaxObjectSize > 0 && estimatedSize > 0 && estimatedSize > p.MaxObjectSize {
		return BypassSizeLimit, true
	}
	if p.ReserveSpace > 0 && p.CacheDir != "" {
		avail := availableBytes(p.CacheDir)
		if avail >= 0 && avail < p.ReserveSpace {
			return BypassDiskLow, true
		}
	}
	return BypassNone, false
}

// Store 是缓存层对外暴露的接口。
type Store interface {
	// OpenStream 打开一个流式写入器（io.Writer），调用方把数据 Write 进去，
	// 最后 Close() 完成落盘（fsync + rename）。
	//
	// 设计动机：proxy 层用 io.TeeReader(resp.Body, w, stream) 同时转发给客户端和写盘；
	// cache 不需要拿到 resp.Body 的 read 端。
	//
	// 行为：
	//   - bypass 在 OpenStream 时根据 estimatedSize 预判；
	//   - bypass 仍会返回一个有效 stream，但 Write 永远不写盘；Close() 直接返回 nil；
	//   - bypass 状态通过 StreamWriter.Bypassed() 暴露。
	OpenStream(registry, repo, digest, mediaType string, estimatedSize int64) (StreamWriter, error)

	// Get 打开本地缓存文件。返回的 ReadSeekCloser 由调用方负责 Close。
	// 不存在返回 ErrNotFound。
	Get(registry, repo, digest string) (io.ReadSeekCloser, int64, error)

	// Stat 检查缓存是否命中（不打开文件）。
	Stat(registry, repo, digest string) (bool, int64, error)

	// Delete 删除缓存条目（文件 + 元数据由调用方负责元数据清理）。
	Delete(registry, repo, digest string) error

	// RootDir 返回缓存根目录（仅供诊断 / 维护用）。
	RootDir() string

	// BypassCheck 预判：给定估算大小，判断是否应旁路（不写盘）。
	// 调用方应在收到上游 Content-Length 后、WriteHeader 之前调用本方法。
	// size <= 0 表示未知，仅做磁盘检查。
	BypassCheck(size int64) (BypassReason, bool)
}

// StreamWriter 是流式写入缓存的接口。
type StreamWriter interface {
	io.Writer
	// Close 完成落盘（fsync + rename）。多次调用安全。
	Close() error
	// Bypassed 返回是否走了旁路（true 时 Close 不会再落盘）。
	Bypassed() bool
	// BypassReason 返回旁路原因（Bypassed=false 时返回空）。
	BypassReason() BypassReason
	// Written 返回已写入字节数（旁路时也累计调用方的 Write）。
	Written() int64
}

// FileStore 是 Store 的本地文件系统实现。
type FileStore struct {
	root  string
	pol   Policy
	fsync bool

	mu sync.RWMutex
}

// NewFileStore 构造 FileStore，自动确保 root 存在。
func NewFileStore(root string, pol Policy) (*FileStore, error) {
	if root == "" {
		return nil, errors.New("cache: root dir is required")
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return nil, fmt.Errorf("cache: mkdir root: %w", err)
	}
	pol.CacheDir = root
	return &FileStore{
		root:  root,
		pol:   pol,
		fsync: true,
	}, nil
}

// RootDir 返回缓存根目录。
func (s *FileStore) RootDir() string { return s.root }

// BypassCheck 实现 Store 接口。
func (s *FileStore) BypassCheck(size int64) (BypassReason, bool) {
	return s.currentPolicy().ShouldBypass(size)
}

// SetPolicy 在运行时更新 Policy（用于响应配置变更或测试）。
// 线程安全。
func (s *FileStore) SetPolicy(p Policy) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if p.CacheDir == "" {
		p.CacheDir = s.root
	}
	s.pol = p
}

// currentPolicy 线程安全读。
func (s *FileStore) currentPolicy() Policy {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.pol
}

// safeRel 把 (registry, repo, digest) 拼成 CacheDir 下的相对路径。
func safeRel(registry, repo, digest string) (string, error) {
	if registry == "" {
		return "", errors.New("cache: registry is empty")
	}
	if digest == "" {
		return "", errors.New("cache: digest is empty")
	}
	if strings.Contains(registry, "..") || strings.ContainsAny(registry, "/\\") {
		return "", fmt.Errorf("cache: invalid registry %q", registry)
	}
	if strings.Contains(repo, "..") {
		return "", fmt.Errorf("cache: invalid repo %q (contains ..)", repo)
	}
	cleanedRepo := filepath.Clean("/" + repo)
	cleanedRepo = strings.TrimPrefix(cleanedRepo, "/")
	if cleanedRepo == "" {
		cleanedRepo = "_empty"
	}
	if !validDigest(digest) {
		return "", fmt.Errorf("cache: invalid digest %q", digest)
	}
	return filepath.Join("v2", registry, cleanedRepo, "blobs", digest), nil
}

// validDigest 简单校验：必须是 <algo>:<hex> 形式。
func validDigest(d string) bool {
	idx := strings.Index(d, ":")
	if idx <= 0 || idx == len(d)-1 {
		return false
	}
	algo := d[:idx]
	hex := d[idx+1:]
	switch algo {
	case "sha256", "sha512", "sha1":
	default:
		return false
	}
	for _, c := range hex {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
			return false
		}
	}
	return true
}

// blobPath 解析 (registry, repo, digest) 到绝对路径。
func (s *FileStore) blobPath(registry, repo, digest string) (string, error) {
	rel, err := safeRel(registry, repo, digest)
	if err != nil {
		return "", err
	}
	abs := filepath.Join(s.root, rel)
	cleanedAbs := filepath.Clean(abs)
	cleanedRoot := filepath.Clean(s.root) + string(os.PathSeparator)
	if !strings.HasPrefix(cleanedAbs, cleanedRoot) {
		return "", fmt.Errorf("cache: path traversal detected: %s", cleanedAbs)
	}
	return cleanedAbs, nil
}

// Stat 检查缓存是否命中。
func (s *FileStore) Stat(registry, repo, digest string) (bool, int64, error) {
	target, err := s.blobPath(registry, repo, digest)
	if err != nil {
		return false, 0, err
	}
	info, err := os.Stat(target)
	if err != nil {
		if os.IsNotExist(err) {
			return false, 0, nil
		}
		return false, 0, fmt.Errorf("cache: stat: %w", err)
	}
	return true, info.Size(), nil
}

// Get 打开本地缓存文件。
func (s *FileStore) Get(registry, repo, digest string) (io.ReadSeekCloser, int64, error) {
	target, err := s.blobPath(registry, repo, digest)
	if err != nil {
		return nil, 0, err
	}
	f, err := os.Open(target)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, 0, ErrNotFound
		}
		return nil, 0, fmt.Errorf("cache: open blob: %w", err)
	}
	info, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, 0, fmt.Errorf("cache: stat blob: %w", err)
	}
	return f, info.Size(), nil
}

// Delete 删除缓存文件。
func (s *FileStore) Delete(registry, repo, digest string) error {
	target, err := s.blobPath(registry, repo, digest)
	if err != nil {
		return err
	}
	if err := os.Remove(target); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("cache: remove: %w", err)
	}
	return nil
}

// OpenStream 实现 Store 接口。
//
// bypass 在这里预判（基于 estimatedSize）。如果 bypass：
//   - 仍返回 *fileStreamWriter（Write 写到内存 blacklist，Close 直接 no-op）
//   - 状态通过 Bypassed() / BypassReason() 暴露
//
// 非 bypass：
//   - 立即开 tmp 文件（带 .tmp.${pid}.${nanos} 后缀）
//   - 每次 Write 直接落盘到 tmp（不缓冲，节省内存）
//   - Close 时 fsync + rename 到目标路径
func (s *FileStore) OpenStream(registry, repo, digest, _ /* mediaType */ string, estimatedSize int64) (StreamWriter, error) {
	pol := s.currentPolicy()
	if reason, bypass := pol.ShouldBypass(estimatedSize); bypass {
		return &fileStreamWriter{
			bypass:   true,
			reason:   reason,
			maxBytes: pol.MaxObjectSize,
		}, nil
	}

	target, err := s.blobPath(registry, repo, digest)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return nil, fmt.Errorf("cache: mkdir blob dir: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(target), filepath.Base(target)+".tmp.*")
	if err != nil {
		return nil, fmt.Errorf("cache: create tmp: %w", err)
	}
	return &fileStreamWriter{
		store:     s,
		tmp:       tmp,
		tmpPath:   tmp.Name(),
		target:    target,
		fsync:     s.fsync,
		maxBytes:  pol.MaxObjectSize,
		createdAt: time.Now(),
	}, nil
}

// fileStreamWriter 是 StreamWriter 的文件实现。
type fileStreamWriter struct {
	store    *FileStore
	tmp      *os.File
	tmpPath  string
	target   string
	fsync    bool
	maxBytes int64

	// 实时统计
	written atomic.Int64

	// 状态
	closed   atomic.Bool
	finished atomic.Bool // fsync + rename 完成
	bypass   bool
	reason   BypassReason

	// 错误（Write / Close 期间）
	errMu sync.Mutex
	err   error

	createdAt time.Time
}

// Write 实现 io.Writer。
//
// 行为：
//   - bypass=true：只累计 written，不落盘
//   - 实时 size 检查：超 maxBytes → 自动转 bypass（关闭 tmp，剩余 Write 不落盘）
//   - 多次 Write 安全（顺序写）
func (w *fileStreamWriter) Write(p []byte) (int, error) {
	if w.bypass {
		w.written.Add(int64(len(p)))
		return len(p), nil
	}
	if w.closed.Load() {
		return 0, errors.New("cache: write after close")
	}
	n, err := w.tmp.Write(p)
	w.written.Add(int64(n))
	if err != nil {
		w.errMu.Lock()
		w.err = err
		w.errMu.Unlock()
		return n, err
	}
	// 实时 size 检查
	if w.maxBytes > 0 && w.written.Load() > w.maxBytes {
		// 升级为 bypass：关 tmp，记 reason
		_ = w.tmp.Close()
		_ = os.Remove(w.tmpPath)
		w.bypass = true
		w.reason = BypassSizeLimit
		w.tmp = nil
		// 继续累计 written（让调用方的统计一致）
	}
	return n, nil
}

// Close 完成落盘（fsync + rename）。多次调用安全。
func (w *fileStreamWriter) Close() error {
	if w.closed.Swap(true) {
		return nil
	}
	if w.bypass {
		w.finished.Store(true)
		return nil
	}
	if w.tmp == nil {
		w.finished.Store(true)
		return nil
	}
	if w.fsync {
		if err := w.tmp.Sync(); err != nil {
			w.errMu.Lock()
			w.err = fmt.Errorf("cache: fsync tmp: %w", err)
			w.errMu.Unlock()
			_ = w.tmp.Close()
			_ = os.Remove(w.tmpPath)
			return w.err
		}
	}
	if err := w.tmp.Close(); err != nil {
		w.errMu.Lock()
		w.err = fmt.Errorf("cache: close tmp: %w", err)
		w.errMu.Unlock()
		return w.err
	}
	if err := os.Rename(w.tmpPath, w.target); err != nil {
		w.errMu.Lock()
		w.err = fmt.Errorf("cache: rename tmp: %w", err)
		w.errMu.Unlock()
		_ = os.Remove(w.tmpPath)
		return w.err
	}
	w.finished.Store(true)
	return nil
}

// Bypassed 返回是否走了旁路。
func (w *fileStreamWriter) Bypassed() bool { return w.bypass }

// BypassReason 返回旁路原因。
func (w *fileStreamWriter) BypassReason() BypassReason { return w.reason }

// Written 返回已写入字节数。
func (w *fileStreamWriter) Written() int64 { return w.written.Load() }

// Put 是 OpenStream + io.Copy + Close 的便捷包装。
//
// 用于直接给 reader 的场景（测试、batch import）。proxy 层应优先用 OpenStream。
//
// 行为：
//   - bypass 时返回 (BypassReason, *BypassError) — 调用方可 IsBypass 判断；
//   - 正常时返回 (BypassNone, nil)。
func (s *FileStore) Put(ctx context.Context, registry, repo, digest, mediaType string, size int64, body io.Reader) (BypassReason, error) {
	sw, err := s.OpenStream(registry, repo, digest, mediaType, size)
	if err != nil {
		return BypassNone, err
	}
	if _, copyErr := io.Copy(sw, body); copyErr != nil {
		_ = sw.Close()
		if errors.Is(copyErr, context.Canceled) {
			return BypassNone, copyErr
		}
		return BypassNone, copyErr
	}
	if closeErr := sw.Close(); closeErr != nil {
		return BypassNone, closeErr
	}
	if sw.Bypassed() {
		return sw.BypassReason(), &BypassError{Reason: sw.BypassReason()}
	}
	return BypassNone, nil
}

// availableBytes 返回目录所在文件系统的可用字节数；不支持时返回 -1。
func availableBytes(dir string) int64 {
	var st unix.Statfs_t
	if err := unix.Statfs(dir, &st); err != nil {
		return -1
	}
	if st.Bavail > uint64(^uint64(0)>>1) {
		return -1
	}
	return int64(st.Bavail) * int64(st.Bsize)
}

// TouchAt 在指定文件上设置 mtime。
func TouchAt(path string, t time.Time) error {
	return os.Chtimes(path, t, t)
}

// 引用 context 包以避免 unused import（Put 用 context.Context）。
var _ = context.Background
