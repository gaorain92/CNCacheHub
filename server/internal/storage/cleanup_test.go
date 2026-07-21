package storage

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

// TestOpen_CleanupTasksSeeds 验证 0001 → 0004 → 0005 跑完后，cleanup_tasks 里有 2 条种子。
func TestOpen_CleanupTasksSeeds(t *testing.T) {
	dir := newTempDataDir(t)
	ctx := context.Background()
	db, err := Open(ctx, dir)
	if err != nil {
		t.Fatalf("Open() error: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	tasks, err := db.ListCleanupTasks(ctx)
	if err != nil {
		t.Fatalf("ListCleanupTasks() error: %v", err)
	}
	if len(tasks) != 2 {
		t.Fatalf("expected 2 seeded cleanup tasks, got %d (%+v)", len(tasks), tasks)
	}
	// 第一条应该是 default-lru
	if tasks[0].Name != "default-lru" || tasks[0].Strategy != "lru" {
		t.Errorf("task[0] = %+v, want name=default-lru strategy=lru", tasks[0])
	}
	if tasks[1].Name != "capacity-cap" || tasks[1].Strategy != "capacity" {
		t.Errorf("task[1] = %+v, want name=capacity-cap strategy=capacity", tasks[1])
	}
}

// TestGetCleanupTaskByID_NoSuchColumn 回归测试：GetCleanupTaskByID 必须用 task_name 列名（不是 name）。
//
// 之前 bug：GetCleanupTaskByID 写了 `SELECT id, name, ...`，DB 列是 task_name，触发
// "no such column: name" 错误 → POST /api/cleanup/tasks/:id/run 全挂。
func TestGetCleanupTaskByID_NoSuchColumn(t *testing.T) {
	dir := newTempDataDir(t)
	ctx := context.Background()
	db, err := Open(ctx, dir)
	if err != nil {
		t.Fatalf("Open() error: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	got, err := db.GetCleanupTaskByID(ctx, 1)
	if err != nil {
		t.Fatalf("GetCleanupTaskByID(1) error: %v", err)
	}
	if got.ID != 1 {
		t.Errorf("ID = %d, want 1", got.ID)
	}
	if got.Name != "default-lru" {
		t.Errorf("Name = %q, want default-lru", got.Name)
	}
	if got.Strategy != "lru" {
		t.Errorf("Strategy = %q, want lru", got.Strategy)
	}
	if !got.Enabled {
		t.Errorf("Enabled = false, want true")
	}

	// 不存在的 id 应返回 ErrNotFound
	if _, err := db.GetCleanupTaskByID(ctx, 9999); err == nil {
		t.Errorf("GetCleanupTaskByID(9999) error = nil, want ErrNotFound")
	}
}

// TestRunLRU_FreesOldEntries 验证 RunLRU 按 last_access_at 阈值删条目。
func TestRunLRU_FreesOldEntries(t *testing.T) {
	dir := newTempDataDir(t)
	ctx := context.Background()
	db, err := Open(ctx, dir)
	if err != nil {
		t.Fatalf("Open() error: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	// 准备 cache_entries：3 条，2 条 last_access_at 1 小时前，1 条刚刚
	old := time.Now().Unix() - 3600
	fresh := time.Now().Unix()
	for i, la := range []int64{old, old, fresh} {
		_, err := db.UpsertCacheEntry(ctx, CacheEntry{
			Registry:     "dockerhub",
			Repository:   "library/test",
			Digest:       "sha256:000000000000000000000000000000000000000000000000000000000000000" + string(rune('0'+i)),
			SizeBytes:    1024,
			LastAccessAt: la,
		})
		if err != nil {
			t.Fatalf("UpsertCacheEntry: %v", err)
		}
	}

	// threshold=60s → 2 条老条目该被删
	report, err := db.RunLRU(ctx, 1, 60, 200)
	if err != nil {
		t.Fatalf("RunLRU: %v", err)
	}
	if report.FreedCount != 2 {
		t.Errorf("FreedCount = %d, want 2", report.FreedCount)
	}
	if report.FreedBytes != 2048 {
		t.Errorf("FreedBytes = %d, want 2048", report.FreedBytes)
	}
	if report.BeforeCount != 3 || report.AfterCount != 1 {
		t.Errorf("BeforeCount/AfterCount = %d/%d, want 3/1", report.BeforeCount, report.AfterCount)
	}
}

// TestRunCapacity_FreesExcess 验证 RunCapacity 删到 ≤ thresholdBytes。
func TestRunCapacity_FreesExcess(t *testing.T) {
	dir := newTempDataDir(t)
	ctx := context.Background()
	db, err := Open(ctx, dir)
	if err != nil {
		t.Fatalf("Open() error: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	// 3 条 1024 字节，全是 fresh
	for i := 0; i < 3; i++ {
		_, err := db.UpsertCacheEntry(ctx, CacheEntry{
			Registry:   "dockerhub",
			Repository: "library/cap",
			Digest:     "sha256:111111111111111111111111111111111111111111111111111111111111111" + string(rune('0'+i)),
			SizeBytes:  1024,
		})
		if err != nil {
			t.Fatalf("UpsertCacheEntry: %v", err)
		}
	}

	// threshold=2048 → 3 条 1024 = 3072 > 2048，删完 3 条让 total=0 ≤ 2048
	report, err := db.RunCapacity(ctx, 2, 2048, 200)
	if err != nil {
		t.Fatalf("RunCapacity: %v", err)
	}
	if report.FreedCount != 3 {
		t.Errorf("FreedCount = %d, want 3", report.FreedCount)
	}
	if report.AfterBytes > 2048 {
		t.Errorf("AfterBytes = %d, want <= 2048", report.AfterBytes)
	}
	if report.AfterCount != 0 {
		t.Errorf("AfterCount = %d, want 0", report.AfterCount)
	}
}

// TestUpdateCleanupTaskLastRun 验证 last_run / last_freed_* 写入。
func TestUpdateCleanupTaskLastRun(t *testing.T) {
	dir := newTempDataDir(t)
	ctx := context.Background()
	db, err := Open(ctx, dir)
	if err != nil {
		t.Fatalf("Open() error: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	if err := db.UpdateCleanupTaskLastRun(ctx, 1, "ok", 4096, 2); err != nil {
		t.Fatalf("UpdateCleanupTaskLastRun: %v", err)
	}
	got, err := db.GetCleanupTaskByID(ctx, 1)
	if err != nil {
		t.Fatalf("GetCleanupTaskByID: %v", err)
	}
	if got.LastStatus != "ok" {
		t.Errorf("LastStatus = %q, want ok", got.LastStatus)
	}
	if got.LastFreedBytes != 4096 {
		t.Errorf("LastFreedBytes = %d, want 4096", got.LastFreedBytes)
	}
	if got.LastFreedCount != 2 {
		t.Errorf("LastFreedCount = %d, want 2", got.LastFreedCount)
	}
	if got.LastRunAt == 0 {
		t.Errorf("LastRunAt = 0, want > 0")
	}
}

// smoke: 防止 Open 后立刻被外部程序打开的文件句柄干扰
var _ = filepath.Join
