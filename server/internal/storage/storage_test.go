package storage

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

// newTempDataDir 创建一个临时数据目录并在 cleanup 时移除。
func newTempDataDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	return dir
}

// TestOpen_CreatesDirAndFile 验证 Open 会自动创建目录和 DB 文件。
func TestOpen_CreatesDirAndFile(t *testing.T) {
	dir := newTempDataDir(t)
	// 故意再嵌一层，验证 MkdirAll。
	dir = filepath.Join(dir, "sub", "data")

	ctx := context.Background()
	db, err := Open(ctx, dir)
	if err != nil {
		t.Fatalf("Open() error: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	// DB 文件应存在。
	if !fileExists(filepath.Join(dir, "cncachehub.db")) {
		t.Errorf("expected cncachehub.db to exist in %s", dir)
	}
	if db.Path != filepath.Join(dir, "cncachehub.db") {
		t.Errorf("Path = %q, want %q", db.Path, filepath.Join(dir, "cncachehub.db"))
	}
	if db.DataDir() != dir {
		t.Errorf("DataDir() = %q, want %q", db.DataDir(), dir)
	}
}

// TestOpen_AppliesMigrations 验证 0001_init.sql 跑了，schema_migrations 表存在。
func TestOpen_AppliesMigrations(t *testing.T) {
	dir := newTempDataDir(t)
	ctx := context.Background()
	db, err := Open(ctx, dir)
	if err != nil {
		t.Fatalf("Open() error: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	applied, err := db.ListAppliedMigrations(ctx)
	if err != nil {
		t.Fatalf("ListAppliedMigrations() error: %v", err)
	}
	if len(applied) == 0 {
		t.Fatal("expected at least one applied migration, got none")
	}
	if applied[0] != "0001_init.sql" {
		t.Errorf("first applied migration = %q, want 0001_init.sql", applied[0])
	}

	// 占位表应存在。
	for _, table := range []string{"schema_migrations", "cache_entries", "request_logs", "cleanup_tasks"} {
		var n int
		row := db.SQLDB.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?`, table)
		if err := row.Scan(&n); err != nil {
			t.Fatalf("query sqlite_master for %s: %v", table, err)
		}
		if n != 1 {
			t.Errorf("table %s missing (count=%d)", table, n)
		}
	}
}

// TestOpen_Idempotent 验证第二次打开同一个目录不会重复应用 migrations。
func TestOpen_Idempotent(t *testing.T) {
	dir := newTempDataDir(t)
	ctx := context.Background()

	db1, err := Open(ctx, dir)
	if err != nil {
		t.Fatalf("first Open() error: %v", err)
	}
	first, err := db1.ListAppliedMigrations(ctx)
	if err != nil {
		t.Fatalf("first ListAppliedMigrations: %v", err)
	}
	if err := db1.Close(); err != nil {
		t.Fatalf("first Close() error: %v", err)
	}

	db2, err := Open(ctx, dir)
	if err != nil {
		t.Fatalf("second Open() error: %v", err)
	}
	t.Cleanup(func() { _ = db2.Close() })

	second, err := db2.ListAppliedMigrations(ctx)
	if err != nil {
		t.Fatalf("second ListAppliedMigrations: %v", err)
	}
	if len(first) != len(second) {
		t.Errorf("migration count changed: first=%d second=%d", len(first), len(second))
	}
	for i := range first {
		if first[i] != second[i] {
			t.Errorf("migration[%d] differs: first=%q second=%q", i, first[i], second[i])
		}
	}
}

// TestOpen_WALEnabled 验证 WAL 模式已启用。
func TestOpen_WALEnabled(t *testing.T) {
	dir := newTempDataDir(t)
	ctx := context.Background()
	db, err := Open(ctx, dir)
	if err != nil {
		t.Fatalf("Open() error: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	var mode string
	if err := db.SQLDB.QueryRowContext(ctx, `PRAGMA journal_mode`).Scan(&mode); err != nil {
		t.Fatalf("PRAGMA journal_mode: %v", err)
	}
	if !strings.EqualFold(mode, "wal") {
		t.Errorf("journal_mode = %q, want wal", mode)
	}
}

// TestOpen_ForeignKeysEnabled 验证 foreign_keys 已开启。
func TestOpen_ForeignKeysEnabled(t *testing.T) {
	dir := newTempDataDir(t)
	ctx := context.Background()
	db, err := Open(ctx, dir)
	if err != nil {
		t.Fatalf("Open() error: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	var fk int
	if err := db.SQLDB.QueryRowContext(ctx, `PRAGMA foreign_keys`).Scan(&fk); err != nil {
		t.Fatalf("PRAGMA foreign_keys: %v", err)
	}
	if fk != 1 {
		t.Errorf("foreign_keys = %d, want 1", fk)
	}
}

// TestPing 验证 Ping 正常。
func TestPing(t *testing.T) {
	dir := newTempDataDir(t)
	ctx := context.Background()
	db, err := Open(ctx, dir)
	if err != nil {
		t.Fatalf("Open() error: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	if err := db.Ping(ctx); err != nil {
		t.Errorf("Ping() error: %v", err)
	}
}

// TestOpen_EmptyDataDir 验证空 dataDir 报错。
func TestOpen_EmptyDataDir(t *testing.T) {
	ctx := context.Background()
	_, err := Open(ctx, "")
	if err == nil {
		t.Fatal("expected error for empty dataDir")
	}
}

// fileExists 简单 stat 包装。
func fileExists(p string) bool {
	if p == "" {
		return false
	}
	// 使用 os.Stat 通过 context-free 调用即可。
	// 这里直接 import 会让测试多一个依赖；用标准库即可。
	return fileExistsOS(p)
}
