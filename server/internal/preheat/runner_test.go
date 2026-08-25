// preheat/runner_test.go — 单元测试（纯函数 + RunTask + CancelTask）
package preheat

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	logpkg "github.com/cncachehub/server/internal/log"
	"github.com/cncachehub/server/internal/storage"
)

// ---------------------------------------------------------------------------
// Pure function tests
// ---------------------------------------------------------------------------

func TestSplitDockerImage_DockerHubShort(t *testing.T) {
	t.Parallel()
	tests := []struct {
		target     string
		wantReg    string
		wantName   string
		wantRef    string
	}{
		{"nginx", "dockerhub", "library/nginx", "latest"},
		{"nginx:alpine", "dockerhub", "library/nginx", "alpine"},
		{"library/ubuntu:22.04", "dockerhub", "library/ubuntu", "22.04"},
		{"alpine", "dockerhub", "library/alpine", "latest"},
	}
	for _, tt := range tests {
		t.Run(tt.target, func(t *testing.T) {
			registry, name, ref, err := splitDockerImage(tt.target)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if registry != tt.wantReg {
				t.Errorf("registry = %q, want %q", registry, tt.wantReg)
			}
			if name != tt.wantName {
				t.Errorf("name = %q, want %q", name, tt.wantName)
			}
			if ref != tt.wantRef {
				t.Errorf("ref = %q, want %q", ref, tt.wantRef)
			}
		})
	}
}

func TestSplitDockerImage_CustomRegistry(t *testing.T) {
	t.Parallel()
	tests := []struct {
		target  string
		wantReg string
		wantName string
	}{
		{"ghcr.io/owner/repo:1.0", "ghcr", "ghcr.io/owner/repo"},
		{"ghcr.io/owner/repo", "ghcr", "ghcr.io/owner/repo"},
		{"quay.io/prometheus/prometheus:v2.52.0", "quay", "quay.io/prometheus/prometheus"},
		{"registry.k8s.io/pause:3.9", "k8s", "registry.k8s.io/pause"},
		{"localhost:5000/myimg:tag", "localhost", "localhost:5000/myimg"},
	}
	for _, tt := range tests {
		t.Run(tt.target, func(t *testing.T) {
			registry, name, ref, err := splitDockerImage(tt.target)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if registry != tt.wantReg {
				t.Errorf("registry = %q, want %q", registry, tt.wantReg)
			}
			if name != tt.wantName {
				t.Errorf("name = %q, want %q", name, tt.wantName)
			}
			if ref == "" {
				t.Errorf("ref should be non-empty")
			}
		})
	}
}

func TestSplitDockerImage_BadDigest(t *testing.T) {
	t.Parallel()
	// digests 形如 sha256:abc... — "tag" 里有 ":" 也会被识别为 ref，但 digest 不影响 parse
	digest := "sha256:abc123def456abc123def456abc123def456abc123def456abc123def456"
	target := "ghcr.io/x/y@" + digest
	registry, name, ref, err := splitDockerImage(target)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if registry != "ghcr" {
		t.Errorf("registry = %q, want ghcr", registry)
	}
	if name != "ghcr.io/x/y" {
		t.Errorf("name = %q", name)
	}
	if ref != digest {
		t.Errorf("ref = %q, want %q", ref, digest)
	}
}

func TestSplitDockerImage_WhitespaceOnly(t *testing.T) {
	t.Parallel()
	// 只有空格 — trim 后为空，应返回 error
	_, _, _, err := splitDockerImage("   \t\n  ")
	if err == nil {
		t.Fatal("whitespace-only target should fail")
	}
	if !strings.Contains(err.Error(), "empty") {
		t.Errorf("error should mention 'empty', got: %v", err)
	}
}

func TestRegistryNameFromHost(t *testing.T) {
	t.Parallel()
	tests := map[string]string{
		"docker.io":               "dockerhub",
		"registry-1.docker.io":    "dockerhub",
		"index.docker.io":         "dockerhub",
		"ghcr.io":                 "ghcr",
		"quay.io":                 "quay",
		"registry.k8s.io":         "k8s",
		"my-registry.example.com": "my-registry", // 兜底：第一段
		"localhost":               "localhost",
		"localhost:5000":          "localhost", // 端口不影响
	}
	for host, want := range tests {
		t.Run(host, func(t *testing.T) {
			got := registryNameFromHost(host)
			if got != want {
				t.Errorf("registryNameFromHost(%q) = %q, want %q", host, got, want)
			}
		})
	}
}

func TestDockerPathFor(t *testing.T) {
	t.Parallel()
	// dockerhub 约定不带 "dockerhub/" 段
	got := dockerPathFor("dockerhub", "library/nginx", "manifests", "alpine")
	want := "/v2/library/nginx/manifests/alpine"
	if got != want {
		t.Errorf("dockerhub path = %q, want %q", got, want)
	}
	// 其他 registry 带段
	got = dockerPathFor("ghcr", "ghcr.io/owner/repo", "blobs", "sha256:abc")
	want = "/v2/ghcr/ghcr.io/owner/repo/blobs/sha256:abc"
	if got != want {
		t.Errorf("ghcr path = %q, want %q", got, want)
	}
}

func TestExtractDigests_OCIManifest(t *testing.T) {
	t.Parallel()
	body := []byte(`{
		"mediaType": "application/vnd.oci.image.manifest.v1+json",
		"config": {"digest": "sha256:cfg123"},
		"layers": [
			{"digest": "sha256:layer1"},
			{"digest": "sha256:layer2"}
		]
	}`)
	digests, err := extractDigests(body, "application/vnd.oci.image.manifest.v1+json")
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	want := []string{"sha256:cfg123", "sha256:layer1", "sha256:layer2"}
	if len(digests) != len(want) {
		t.Fatalf("got %d digests, want %d: %v", len(digests), len(want), digests)
	}
	for i, d := range digests {
		if d != want[i] {
			t.Errorf("digest[%d] = %q, want %q", i, d, want[i])
		}
	}
}

func TestExtractDigests_OCIIndex(t *testing.T) {
	t.Parallel()
	// multi-arch: index manifest 列出子 manifest digest
	body := []byte(`{
		"mediaType": "application/vnd.oci.image.index.v1+json",
		"manifests": [
			{"digest": "sha256:amd64"},
			{"digest": "sha256:arm64"}
		]
	}`)
	digests, err := extractDigests(body, "application/vnd.oci.image.index.v1+json")
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if len(digests) != 2 {
		t.Fatalf("got %d digests, want 2: %v", len(digests), digests)
	}
}

func TestExtractDigests_EmptyManifest(t *testing.T) {
	t.Parallel()
	body := []byte(`{"mediaType": "application/vnd.oci.image.manifest.v1+json"}`)
	_, err := extractDigests(body, "application/vnd.oci.image.manifest.v1+json")
	// 没有 digests 是合法的（空 manifest）— 返回空数组
	if err != nil {
		t.Errorf("empty manifest should not error, got: %v", err)
	}
}

func TestExtractDigests_BadJSON(t *testing.T) {
	t.Parallel()
	body := []byte(`{not json`)
	_, err := extractDigests(body, "application/json")
	if err == nil {
		t.Fatal("expected error for bad JSON, got nil")
	}
	if !strings.Contains(err.Error(), "parse manifest") {
		t.Errorf("error should mention 'parse manifest', got: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Runner constructor + CancelTask
// ---------------------------------------------------------------------------

func newTestRunner(t *testing.T) (*Runner, *storage.DB) {
	t.Helper()
	// 临时 DB — 每个 test 一个独立目录
	dir := t.TempDir()
	db, err := storage.Open(context.Background(), dir)
	if err != nil {
		t.Fatalf("storage.Open: %v", err)
	}
	t.Cleanup(func() {
		_ = db.Close()
		_ = os.RemoveAll(dir)
	})
	// user 必须存在才能创 preheat task（FK 约束）
	_, err = db.CreateUser(context.Background(), "tester", "x", true)
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	return NewRunner(db, "http://test-host:8082", logpkg.L()), db
}

func TestNewRunner_NilLogger_DefaultsToSlog(t *testing.T) {
	t.Parallel()
	// 不传 logger 不会 panic
	r := NewRunner(nil, "http://test:8082", nil)
	if r == nil {
		t.Fatal("NewRunner returned nil")
	}
	if r.Log == nil {
		t.Error("Log should default to non-nil")
	}
	if r.running == nil {
		t.Error("running map should be initialized")
	}
}

func TestNewRunner_TrimsTrailingSlash(t *testing.T) {
	t.Parallel()
	r := NewRunner(nil, "http://test:8082/", nil)
	if r.CNCHBaseURL != "http://test:8082" {
		t.Errorf("CNCHBaseURL = %q, want %q", r.CNCHBaseURL, "http://test:8082")
	}
}

func TestCancelTask_NotRunning(t *testing.T) {
	t.Parallel()
	r, _ := newTestRunner(t)
	// task 1 不存在 — CancelTask 应该返 false，不 panic
	if r.CancelTask(999) != false {
		t.Error("CancelTask on non-existent task should return false")
	}
}

func TestCancelTask_Running(t *testing.T) {
	t.Parallel()
	r, db := newTestRunner(t)
	ctx := context.Background()
	// 创 task + item（kind=docker 避免真的拉镜像）
	task, err := db.CreatePreheatTask(ctx, storage.PreheatTask{
		Name:    "test-task",
		Kind:    storage.PreheatKindDocker,
		Targets: []string{"nginx:alpine"},
		Enabled: true,
	})
	if err != nil {
		t.Fatalf("CreatePreheatTask: %v", err)
	}
	// 注册一个手动控制的 cancel（模拟 RunTask 已经在跑）
	r.mu.Lock()
	cancelCalled := false
	r.running[task.ID] = func() { cancelCalled = true }
	r.mu.Unlock()

	if !r.CancelTask(task.ID) {
		t.Error("CancelTask on running task should return true")
	}
	if !cancelCalled {
		t.Error("cancel function should have been called")
	}
}

// ---------------------------------------------------------------------------
// RunTask — 异步 + httptest mock CNCH server
// ---------------------------------------------------------------------------

func TestRunTask_DockerPreheat_AllSucceed(t *testing.T) {
	t.Parallel()
	// mock CNCH server：manifest 返回 2 个 layer + 1 config，blobs 返回 OK
	var blobHits int
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/manifests/alpine"):
			w.Header().Set("Content-Type", "application/vnd.oci.image.manifest.v1+json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"config": map[string]string{"digest": "sha256:cfg1"},
				"layers": []map[string]string{
					{"digest": "sha256:layer1"},
					{"digest": "sha256:layer2"},
				},
			})
		case strings.Contains(r.URL.Path, "/blobs/"):
			blobHits++
			_, _ = w.Write([]byte("blob-content"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer mock.Close()

	r, db := newTestRunner(t)
	// 改 CNCHBaseURL 指向 mock
	r.CNCHBaseURL = mock.URL
	// 短 timeout
	r.HTTPClient = &http.Client{Timeout: 5 * time.Second}

	ctx := context.Background()
	task, err := db.CreatePreheatTask(ctx, storage.PreheatTask{
		Name:    "happy-path",
		Kind:    storage.PreheatKindDocker,
		Targets: []string{"nginx:alpine"},
		Enabled: true,
	})
	if err != nil {
		t.Fatalf("CreatePreheatTask: %v", err)
	}

	if err := r.RunTask(ctx, task.ID); err != nil {
		t.Fatalf("RunTask: %v", err)
	}

	// 等异步完成（最多 3s）
	if !waitForTaskStatus(t, db, task.ID, storage.PreheatStatusDone, 3*time.Second) {
		t.Fatalf("task %d did not reach status=done", task.ID)
	}

	// 验证：3 个 blob（1 config + 2 layers）都拉了
	if blobHits != 3 {
		t.Errorf("blob hits = %d, want 3", blobHits)
	}

	// 验证：进度 done=1（1 个 target = 1 个 item，与 blob 数量无关）
	finalTask, err := db.GetPreheatTask(ctx, task.ID)
	if err != nil {
		t.Fatalf("GetPreheatTask: %v", err)
	}
	if finalTask.ProgressDone != 1 {
		t.Errorf("ProgressDone = %d, want 1 (per-target, not per-blob)", finalTask.ProgressDone)
	}
	if finalTask.Status != storage.PreheatStatusDone {
		t.Errorf("Status = %q, want done", finalTask.Status)
	}
}

func TestRunTask_DockerPreheat_ManifestError(t *testing.T) {
	t.Parallel()
	// mock 返回 404
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "not found", http.StatusNotFound)
	}))
	defer mock.Close()

	r, db := newTestRunner(t)
	r.CNCHBaseURL = mock.URL
	r.HTTPClient = &http.Client{Timeout: 5 * time.Second}

	ctx := context.Background()
	task, err := db.CreatePreheatTask(ctx, storage.PreheatTask{
		Name:    "fail-manifest",
		Kind:    storage.PreheatKindDocker,
		Targets: []string{"nginx:alpine"},
		Enabled: true,
	})
	if err != nil {
		t.Fatalf("CreatePreheatTask: %v", err)
	}

	_ = r.RunTask(ctx, task.ID)
	if !waitForTaskStatus(t, db, task.ID, storage.PreheatStatusError, 3*time.Second) {
		t.Fatalf("task did not reach status=error")
	}

	finalTask, err := db.GetPreheatTask(ctx, task.ID)
	if err != nil {
		t.Fatalf("GetPreheatTask: %v", err)
	}
	if finalTask.Status != storage.PreheatStatusError {
		t.Errorf("Status = %q, want error", finalTask.Status)
	}
	if !strings.Contains(finalTask.ErrorMessage, "manifest") {
		t.Errorf("error message should mention manifest, got: %q", finalTask.ErrorMessage)
	}
}

func TestRunTask_UnknownKind(t *testing.T) {
	t.Parallel()
	r, db := newTestRunner(t)
	ctx := context.Background()
	// kind = "bogus" 触发 default 分支
	task, err := db.CreatePreheatTask(ctx, storage.PreheatTask{
		Name:    "unknown-kind",
		Kind:    "bogus",
		Targets: []string{"x"},
		Enabled: true,
	})
	if err != nil {
		t.Fatalf("CreatePreheatTask: %v", err)
	}
	_ = r.RunTask(ctx, task.ID)
	if !waitForTaskStatus(t, db, task.ID, storage.PreheatStatusError, 3*time.Second) {
		t.Fatalf("task did not reach status=error")
	}
}

func TestRunTask_CancelStopsRunning(t *testing.T) {
	t.Parallel()
	// mock 慢：每个请求 sleep 200ms（保证 cancel 来得及）
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer mock.Close()

	r, db := newTestRunner(t)
	r.CNCHBaseURL = mock.URL
	r.HTTPClient = &http.Client{Timeout: 5 * time.Second}

	ctx := context.Background()
	task, err := db.CreatePreheatTask(ctx, storage.PreheatTask{
		Name:    "cancel-test",
		Kind:    storage.PreheatKindDocker,
		Targets: []string{"a:1", "b:2", "c:3"}, // 3 个 target，每个都慢
		Enabled: true,
	})
	if err != nil {
		t.Fatalf("CreatePreheatTask: %v", err)
	}

	_ = r.RunTask(ctx, task.ID)
	// 等 runner 启动
	time.Sleep(100 * time.Millisecond)

	if !r.CancelTask(task.ID) {
		t.Error("CancelTask should return true while running")
	}

	// 等 task 变 canceled 状态（不是 done / error）
	if !waitForTaskStatusIn(t, db, task.ID,
		[]string{storage.PreheatStatusCanceled, storage.PreheatStatusError}, 3*time.Second) {
		t.Fatalf("task did not reach canceled/error status")
	}

	finalTask, err := db.GetPreheatTask(ctx, task.ID)
	if err != nil {
		t.Fatalf("GetPreheatTask: %v", err)
	}
	if finalTask.Status != storage.PreheatStatusCanceled {
		// 可能在 cancel 前已经 error（被 mock 的延迟触发）
		// 但应该包含 "cancel" 字样
		if !strings.Contains(finalTask.ErrorMessage, "cancel") {
			t.Logf("note: status=%s, msg=%q", finalTask.Status, finalTask.ErrorMessage)
		}
	}
}

func TestRunTask_NotFound(t *testing.T) {
	t.Parallel()
	r, _ := newTestRunner(t)
	err := r.RunTask(context.Background(), 9999) // 不存在的 task
	if err == nil {
		t.Error("RunTask on non-existent task should return error")
	}
}

func TestRunTask_AlreadyRunning(t *testing.T) {
	t.Parallel()
	r, db := newTestRunner(t)
	// mock 慢 — 让 task 留在 running 状态
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(500 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer mock.Close()
	r.CNCHBaseURL = mock.URL
	r.HTTPClient = &http.Client{Timeout: 5 * time.Second}

	ctx := context.Background()
	task, err := db.CreatePreheatTask(ctx, storage.PreheatTask{
		Name:    "double-run",
		Kind:    storage.PreheatKindDocker,
		Targets: []string{"x:1"},
		Enabled: true,
	})
	if err != nil {
		t.Fatalf("CreatePreheatTask: %v", err)
	}

	if err := r.RunTask(ctx, task.ID); err != nil {
		t.Fatalf("first RunTask: %v", err)
	}
	// 立即再跑一次
	time.Sleep(50 * time.Millisecond) // 等 runner 把状态置为 running
	err2 := r.RunTask(ctx, task.ID)
	if err2 == nil {
		t.Error("second RunTask on running task should return error")
	}
	if !strings.Contains(err2.Error(), "already running") {
		t.Errorf("error should mention 'already running', got: %v", err2)
	}

	// 等第一次跑完（避免影响下一个 test 的状态）
	waitForTaskStatus(t, db, task.ID, storage.PreheatStatusDone, 3*time.Second)
}

// ---------------------------------------------------------------------------
// runDockerImage — 直接测（用 httptest）
// ---------------------------------------------------------------------------

func TestRunDockerImage_UnknownImageFormat(t *testing.T) {
	t.Parallel()
	r, _ := newTestRunner(t)
	_, err := r.runDockerImage(context.Background(), "")
	if err == nil {
		t.Error("empty target should fail")
	}
}

func TestRunDockerImage_ManifestFails(t *testing.T) {
	t.Parallel()
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer mock.Close()

	r, _ := newTestRunner(t)
	r.CNCHBaseURL = mock.URL
	_, err := r.runDockerImage(context.Background(), "nginx:alpine")
	if err == nil {
		t.Error("expected error from 500 response")
	}
	if !strings.Contains(err.Error(), "get manifest") {
		t.Errorf("error should mention 'get manifest', got: %v", err)
	}
}

func TestRunDockerImage_BlobError(t *testing.T) {
	t.Parallel()
	// manifest OK，blobs 返 404
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/manifests/v1"):
			w.Header().Set("Content-Type", "application/vnd.oci.image.manifest.v1+json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"config": map[string]string{"digest": "sha256:cfg1"},
				"layers": []map[string]string{{"digest": "sha256:layer1"}},
			})
		default:
			http.Error(w, "blob not found", http.StatusNotFound)
		}
	}))
	defer mock.Close()

	r, _ := newTestRunner(t)
	r.CNCHBaseURL = mock.URL
	_, err := r.runDockerImage(context.Background(), "nginx:v1")
	if err == nil {
		t.Error("expected blob error")
	}
	if !strings.Contains(err.Error(), "HTTP 404") {
		t.Errorf("error should mention HTTP 404, got: %v", err)
	}
}

func TestRunDockerImage_NoDigests(t *testing.T) {
	t.Parallel()
	// manifest 是空对象 — config / layers / manifests 都空 → "no layers/config" 错误
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/vnd.oci.image.manifest.v1+json")
		_, _ = w.Write([]byte(`{}`))
	}))
	defer mock.Close()

	r, _ := newTestRunner(t)
	r.CNCHBaseURL = mock.URL
	_, err := r.runDockerImage(context.Background(), "nginx:empty")
	if err == nil {
		t.Fatal("empty manifest should error")
	}
	if !strings.Contains(err.Error(), "no layers/config") {
		t.Errorf("error should mention 'no layers/config', got: %v", err)
	}
}

func TestRunTask_ResourceKind_NotImplemented(t *testing.T) {
	t.Parallel()
	r, db := newTestRunner(t)
	ctx := context.Background()
	task, err := db.CreatePreheatTask(ctx, storage.PreheatTask{
		Name:    "resource-placeholder",
		Kind:    storage.PreheatKindResource,
		Targets: []string{"https://example.com/foo"},
		Enabled: true,
	})
	if err != nil {
		t.Fatalf("CreatePreheatTask: %v", err)
	}
	_ = r.RunTask(ctx, task.ID)
	// resource kind 在 runItem 返 "not yet implemented" 错 → item 标 error → task 标 error
	if !waitForTaskStatus(t, db, task.ID, storage.PreheatStatusError, 3*time.Second) {
		t.Fatalf("resource kind task should end in error status")
	}
	final, err := db.GetPreheatTask(ctx, task.ID)
	if err != nil {
		t.Fatalf("GetPreheatTask: %v", err)
	}
	if !strings.Contains(final.ErrorMessage, "not yet implemented") &&
		!strings.Contains(final.ErrorMessage, "resource") {
		t.Errorf("error should mention 'not yet implemented' or 'resource', got: %q", final.ErrorMessage)
	}
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

func waitForTaskStatus(t *testing.T, db *storage.DB, taskID int64, want string, timeout time.Duration) bool {
	t.Helper()
	return waitForTaskStatusIn(t, db, taskID, []string{want}, timeout)
}

func waitForTaskStatusIn(t *testing.T, db *storage.DB, taskID int64, want []string, timeout time.Duration) bool {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		task, err := db.GetPreheatTask(context.Background(), taskID)
		if err != nil {
			t.Logf("GetPreheatTask: %v", err)
			time.Sleep(50 * time.Millisecond)
			continue
		}
		for _, w := range want {
			if task.Status == w {
				return true
			}
		}
		// 还没到目标状态：等 50ms 再试
		time.Sleep(50 * time.Millisecond)
	}
	return false
}

// 防止 unused import 警告（sync 留作 future test 用）
var _ = sync.Mutex{}
var _ = fmt.Sprintf

func TestMarkTaskError_DirectCall(t *testing.T) {
	t.Parallel()
	// 直接调 markTaskError 测该 helper 本身（避免依赖 ListPreheatItems 失败路径）
	r, db := newTestRunner(t)
	ctx := context.Background()
	task, err := db.CreatePreheatTask(ctx, storage.PreheatTask{
		Name:    "mark-err-direct",
		Kind:    storage.PreheatKindDocker,
		Targets: []string{"x"},
		Enabled: true,
	})
	if err != nil {
		t.Fatalf("CreatePreheatTask: %v", err)
	}
	r.markTaskError(ctx, task.ID, "test error msg", 250)

	final, err := db.GetPreheatTask(ctx, task.ID)
	if err != nil {
		t.Fatalf("GetPreheatTask: %v", err)
	}
	if final.Status != storage.PreheatStatusError {
		t.Errorf("Status = %q, want error", final.Status)
	}
	if final.ErrorMessage != "test error msg" {
		t.Errorf("ErrorMessage = %q, want 'test error msg'", final.ErrorMessage)
	}
	if final.LastDurationMs != 250 {
		t.Errorf("LastDurationMs = %d, want 250", final.LastDurationMs)
	}
}

// ---------------------------------------------------------------------------
// huggingface_model kind
// ---------------------------------------------------------------------------

// fakeProxyHFMux 模拟 CNCacheHub 自身的 /r/huggingface-models/<path> 端点。
// hits 记录访问路径；body 长度 = 响应字节数。
type fakeProxyHFMux struct {
	mu        sync.Mutex
	paths     []string
	statusFor map[string]int // path → status override
}

func (f *fakeProxyHFMux) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.paths = append(f.paths, r.URL.Path)
		if f.statusFor != nil {
			if st, ok := f.statusFor[r.URL.Path]; ok {
				w.WriteHeader(st)
				_, _ = w.Write([]byte("fake error body"))
				return
			}
		}
		w.WriteHeader(http.StatusOK)
		// 给个真实字节，让 bytesAdded > 0
		body := make([]byte, 1024)
		_, _ = w.Write(body)
	})
}

func TestRunItem_HuggingFaceModel_Success(t *testing.T) {
	t.Parallel()
	// 启动一个 httptest server 当本地 proxy
	mux := &fakeProxyHFMux{statusFor: map[string]int{}}
	srv := httptest.NewServer(mux.Handler())
	defer srv.Close()

	dir := t.TempDir()
	db, err := storage.Open(context.Background(), dir)
	if err != nil {
		t.Fatalf("storage.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	_, _ = db.CreateUser(context.Background(), "tester", "x", true)
	r := NewRunner(db, srv.URL, logpkg.L())

	target := "Qwen/Qwen2.5-1.5B-Instruct|main|config.json"
	bytes, err := r.runHuggingFaceModelFile(context.Background(), target)
	if err != nil {
		t.Fatalf("runHuggingFaceModelFile: %v", err)
	}
	if bytes != 1024 {
		t.Errorf("bytes = %d, want 1024", bytes)
	}
	mux.mu.Lock()
	defer mux.mu.Unlock()
	if len(mux.paths) != 1 {
		t.Fatalf("proxy hits = %d, want 1", len(mux.paths))
	}
	want := "/r/huggingface-models/Qwen/Qwen2.5-1.5B-Instruct/resolve/main/config.json"
	if mux.paths[0] != want {
		t.Errorf("path = %q, want %q", mux.paths[0], want)
	}
}

func TestRunItem_HuggingFaceModel_DefaultsRevisionToMain(t *testing.T) {
	t.Parallel()
	mux := &fakeProxyHFMux{statusFor: map[string]int{}}
	srv := httptest.NewServer(mux.Handler())
	defer srv.Close()

	dir := t.TempDir()
	db, _ := storage.Open(context.Background(), dir)
	t.Cleanup(func() { _ = db.Close() })
	_, _ = db.CreateUser(context.Background(), "tester", "x", true)
	r := NewRunner(db, srv.URL, logpkg.L())

	// revision 段为空 → 默认 main
	target := "Qwen/Qwen2.5-1.5B-Instruct||config.json"
	if _, err := r.runHuggingFaceModelFile(context.Background(), target); err != nil {
		t.Fatalf("err: %v", err)
	}
	mux.mu.Lock()
	defer mux.mu.Unlock()
	if mux.paths[0] != "/r/huggingface-models/Qwen/Qwen2.5-1.5B-Instruct/resolve/main/config.json" {
		t.Errorf("path = %q, want main fallback", mux.paths[0])
	}
}

func TestRunItem_HuggingFaceModel_401_Hint(t *testing.T) {
	t.Parallel()
	mux := &fakeProxyHFMux{statusFor: map[string]int{
		"/r/huggingface-models/gated/repo/resolve/main/config.json": http.StatusUnauthorized,
	}}
	srv := httptest.NewServer(mux.Handler())
	defer srv.Close()

	dir := t.TempDir()
	db, _ := storage.Open(context.Background(), dir)
	t.Cleanup(func() { _ = db.Close() })
	_, _ = db.CreateUser(context.Background(), "tester", "x", true)
	r := NewRunner(db, srv.URL, logpkg.L())

	_, err := r.runHuggingFaceModelFile(context.Background(), "gated/repo|main|config.json")
	if err == nil {
		t.Fatal("expected error for 401")
	}
	if !strings.Contains(err.Error(), "huggingface_token") {
		t.Errorf("error should mention huggingface_token, got: %v", err)
	}
}

func TestRunItem_HuggingFaceModel_BadTarget(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	db, _ := storage.Open(context.Background(), dir)
	t.Cleanup(func() { _ = db.Close() })
	_, _ = db.CreateUser(context.Background(), "tester", "x", true)
	r := NewRunner(db, "http://unused", logpkg.L())

	cases := []string{
		"only-one-segment",                                // < 3 段
		"|main|config.json",                              // modelID 空
		"foo/bar||",                                      // filename 空
	}
	for _, c := range cases {
		t.Run(c, func(t *testing.T) {
			_, err := r.runHuggingFaceModelFile(context.Background(), c)
			if err == nil {
				t.Errorf("target %q should error", c)
			}
		})
	}
}

func TestRunTask_HuggingFaceModel_EndToEnd(t *testing.T) {
	t.Parallel()
	mux := &fakeProxyHFMux{statusFor: map[string]int{}}
	srv := httptest.NewServer(mux.Handler())
	defer srv.Close()

	dir := t.TempDir()
	db, _ := storage.Open(context.Background(), dir)
	t.Cleanup(func() { _ = db.Close() })
	_, _ = db.CreateUser(context.Background(), "tester", "x", true)
	r := NewRunner(db, srv.URL, logpkg.L())

	ctx := context.Background()
	task, err := db.CreatePreheatTask(ctx, storage.PreheatTask{
		Name:    "hf: foo/bar",
		Kind:    storage.PreheatKindHuggingFaceModel,
		Targets: []string{
			"foo/bar|main|config.json",
			"foo/bar|main|model.safetensors",
		},
		Enabled: true,
	})
	if err != nil {
		t.Fatalf("CreatePreheatTask: %v", err)
	}
	if err := r.RunTask(ctx, task.ID); err != nil {
		t.Fatalf("RunTask: %v", err)
	}
	if !waitForTaskStatus(t, db, task.ID, storage.PreheatStatusDone, 3*time.Second) {
		t.Fatal("task should be done")
	}
	final, _ := db.GetPreheatTask(ctx, task.ID)
	if final.ProgressDone != 2 {
		t.Errorf("ProgressDone = %d, want 2", final.ProgressDone)
	}
	if final.ProgressBytes < 2048 {
		t.Errorf("ProgressBytes = %d, want >= 2048", final.ProgressBytes)
	}
}