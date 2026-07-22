package storage

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestOpen_Phase1MigrationsApplied 验证 0001 + 0002 都跑了，schema 完整。
func TestOpen_Phase1MigrationsApplied(t *testing.T) {
	dir := newTempDataDir(t)
	ctx := context.Background()
	db, err := Open(ctx, dir)
	if err != nil {
		t.Fatalf("Open() error: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	applied, err := db.ListAppliedMigrations(ctx)
	if err != nil {
		t.Fatalf("ListAppliedMigrations: %v", err)
	}
	// 至少应包含 0001 + 0002
	wantSubs := []string{"0001_init.sql", "0002_phase1_cache.sql"}
	for _, w := range wantSubs {
		found := false
		for _, a := range applied {
			if a == w {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("migration %s not applied; applied=%v", w, applied)
		}
	}

	// registry_upstreams 应存在
	var n int
	row := db.SQLDB.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='registry_upstreams'`)
	if err := row.Scan(&n); err != nil {
		t.Fatalf("query registry_upstreams: %v", err)
	}
	if n != 1 {
		t.Errorf("registry_upstreams table missing")
	}
}

// TestOpen_RegistryUpstreamsSeed 验证启动种子 dockerhub 已插入。
func TestOpen_RegistryUpstreamsSeed(t *testing.T) {
	dir := newTempDataDir(t)
	ctx := context.Background()
	db, err := Open(ctx, dir)
	if err != nil {
		t.Fatalf("Open() error: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	var (
		name    string
		upURL   string
		mirror  string
		enabled int
	)
	row := db.SQLDB.QueryRowContext(ctx,
		`SELECT name, upstream_url, mirror_path, enabled FROM registry_upstreams WHERE name = 'dockerhub'`)
	if err := row.Scan(&name, &upURL, &mirror, &enabled); err != nil {
		t.Fatalf("query dockerhub upstream: %v", err)
	}
	if upURL != "https://registry-1.docker.io" {
		t.Errorf("upstream_url = %q, want https://registry-1.docker.io", upURL)
	}
	if mirror != "" {
		// 0009 把 dockerhub.mirror_path 改写成 "" 表示"默认 upstream"
		t.Errorf("mirror_path = %q, want empty (0009 changes dockerhub to default)", mirror)
	}
	if enabled != 1 {
		t.Errorf("enabled = %d, want 1", enabled)
	}
}

// TestCacheEntries_Schema 验证 0002 扩展的 cache_entries 字段能正常 INSERT。
func TestCacheEntries_Schema(t *testing.T) {
	dir := newTempDataDir(t)
	ctx := context.Background()
	db, err := Open(ctx, dir)
	if err != nil {
		t.Fatalf("Open() error: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	now := time.Now().Unix()
	_, err = db.SQLDB.ExecContext(ctx, `
		INSERT INTO cache_entries
		(registry, repository, digest, media_type, size_bytes, storage_path,
		 hit_count, last_access_at, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, "dockerhub", "library/nginx", "sha256:abc", "application/vnd.docker.image.rootfs.diff.tar.gzip",
		12345, "v2/dockerhub/library/nginx/blobs/sha256:abc", 0, now, now)
	if err != nil {
		t.Fatalf("INSERT cache_entries: %v", err)
	}

	// 读回验证
	var (
		reg, repo, dig, mt, sp string
		size, hits             int
	)
	row := db.SQLDB.QueryRowContext(ctx,
		`SELECT registry, repository, digest, media_type, size_bytes, storage_path, hit_count
		 FROM cache_entries WHERE digest = 'sha256:abc'`)
	if err := row.Scan(&reg, &repo, &dig, &mt, &size, &sp, &hits); err != nil {
		t.Fatalf("SELECT: %v", err)
	}
	if reg != "dockerhub" || repo != "library/nginx" || dig != "sha256:abc" {
		t.Errorf("data mismatch: %s/%s/%s", reg, repo, dig)
	}
	if size != 12345 {
		t.Errorf("size_bytes = %d, want 12345", size)
	}

	// 第二次 INSERT 同样 (registry, repository, digest) 应报 UNIQUE 冲突
	_, err = db.SQLDB.ExecContext(ctx, `
		INSERT INTO cache_entries
		(registry, repository, digest, media_type, size_bytes, storage_path, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`, "dockerhub", "library/nginx", "sha256:abc", "x", 1, "y", now)
	if err == nil {
		t.Fatal("expected UNIQUE constraint failure on duplicate digest")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "unique") {
		t.Errorf("expected UNIQUE error, got: %v", err)
	}
}

// TestRequestLogs_Schema 验证 request_logs 字段能正常写入。
func TestRequestLogs_Schema(t *testing.T) {
	dir := newTempDataDir(t)
	ctx := context.Background()
	db, err := Open(ctx, dir)
	if err != nil {
		t.Fatalf("Open() error: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	now := time.Now().Unix()
	_, err = db.SQLDB.ExecContext(ctx, `
		INSERT INTO request_logs
		(created_at, method, path, status, duration_ms, cached, bypassed, client_ip, bytes, error)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, now, "GET", "/v2/library/nginx/manifests/latest", 200, 45, 0, 0, "1.2.3.4", 1024, "")
	if err != nil {
		t.Fatalf("INSERT request_logs: %v", err)
	}

	var status, dur, cached, bypassed, bytes int
	var path, ip string
	row := db.SQLDB.QueryRowContext(ctx,
		`SELECT status, duration_ms, cached, bypassed, bytes, path, client_ip
		 FROM request_logs WHERE created_at = ?`, now)
	if err := row.Scan(&status, &dur, &cached, &bypassed, &bytes, &path, &ip); err != nil {
		t.Fatalf("SELECT: %v", err)
	}
	if status != 200 || dur != 45 || cached != 0 || bypassed != 0 {
		t.Errorf("mismatch: status=%d dur=%d cached=%d bypassed=%d", status, dur, cached, bypassed)
	}
	if path != "/v2/library/nginx/manifests/latest" {
		t.Errorf("path = %q", path)
	}
	if ip != "1.2.3.4" {
		t.Errorf("client_ip = %q", ip)
	}
	if bytes != 1024 {
		t.Errorf("bytes = %d, want 1024", bytes)
	}
}

// TestOpen_Phase1Idempotent 验证 0001+0002 都跑过后，第二次打开不会再跑。
func TestOpen_Phase1Idempotent(t *testing.T) {
	dir := newTempDataDir(t)
	ctx := context.Background()

	db1, err := Open(ctx, dir)
	if err != nil {
		t.Fatalf("first Open: %v", err)
	}
	first, _ := db1.ListAppliedMigrations(ctx)
	if err := db1.Close(); err != nil {
		t.Fatalf("first Close: %v", err)
	}

	// 第二次打开
	db2, err := Open(ctx, dir)
	if err != nil {
		t.Fatalf("second Open: %v", err)
	}
	t.Cleanup(func() { _ = db2.Close() })
	second, _ := db2.ListAppliedMigrations(ctx)

	if len(first) != len(second) {
		t.Errorf("migration count changed: first=%v second=%v", first, second)
	}

	// 种子不应该重复：dockerhub 应该只有 1 条
	var n int
	row := db2.SQLDB.QueryRowContext(ctx, `SELECT COUNT(*) FROM registry_upstreams WHERE name='dockerhub'`)
	if err := row.Scan(&n); err != nil {
		t.Fatalf("count dockerhub: %v", err)
	}
	if n != 1 {
		t.Errorf("dockerhub count = %d, want 1", n)
	}
}

// 兜底：filepath import 保留供未来使用
var _ = filepath.Join
