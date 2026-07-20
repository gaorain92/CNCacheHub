package storage

import (
	"context"
	"testing"
	"time"
)

func TestInsertAndListAccessLog(t *testing.T) {
	dir := newTempDataDir(t)
	ctx := context.Background()
	db, err := Open(ctx, dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	now := time.Now().Unix()
	rec := AccessLogRecord{
		CreatedAt:  now,
		Method:     "GET",
		Path:       "/v2/library/nginx/manifests/latest",
		Status:     200,
		DurationMs: 45,
		Cached:     false,
		Bypassed:   false,
		ClientIP:   "1.2.3.4",
		Bytes:      1234,
		Error:      "",
	}
	if err := db.InsertAccessLog(ctx, rec); err != nil {
		t.Fatalf("InsertAccessLog: %v", err)
	}

	logs, total, err := db.ListAccessLogs(ctx, 1, 10)
	if err != nil {
		t.Fatalf("ListAccessLogs: %v", err)
	}
	if total != 1 {
		t.Errorf("total = %d, want 1", total)
	}
	if len(logs) != 1 {
		t.Fatalf("len(logs) = %d, want 1", len(logs))
	}
	got := logs[0]
	if got.Method != "GET" || got.Path != rec.Path || got.Status != 200 {
		t.Errorf("mismatch: %+v", got)
	}
	if got.Cached || got.Bypassed {
		t.Errorf("expected cached=false bypassed=false, got cached=%v bypassed=%v", got.Cached, got.Bypassed)
	}
}

func TestListAccessLogs_Pagination(t *testing.T) {
	dir := newTempDataDir(t)
	ctx := context.Background()
	db, err := Open(ctx, dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	now := time.Now().Unix()
	for i := 0; i < 25; i++ {
		if err := db.InsertAccessLog(ctx, AccessLogRecord{
			CreatedAt: now + int64(i),
			Method:    "GET",
			Path:      "/v2/foo",
			Status:    200,
		}); err != nil {
			t.Fatalf("insert %d: %v", i, err)
		}
	}

	logs, total, err := db.ListAccessLogs(ctx, 1, 10)
	if err != nil {
		t.Fatalf("ListAccessLogs: %v", err)
	}
	if total != 25 {
		t.Errorf("total = %d, want 25", total)
	}
	if len(logs) != 10 {
		t.Errorf("page 1 size = %d, want 10", len(logs))
	}
}

func TestDashboardSummary_Empty(t *testing.T) {
	dir := newTempDataDir(t)
	ctx := context.Background()
	db, err := Open(ctx, dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	s, err := db.DashboardSummary(ctx)
	if err != nil {
		t.Fatalf("DashboardSummary: %v", err)
	}
	if s.CacheEntries != 0 {
		t.Errorf("CacheEntries = %d, want 0", s.CacheEntries)
	}
	if s.ActiveUpstreams != 1 {
		t.Errorf("ActiveUpstreams = %d, want 1 (seed dockerhub)", s.ActiveUpstreams)
	}
	if s.GeneratedAt == 0 {
		t.Errorf("GeneratedAt = 0")
	}
}

func TestDashboardSummary_WithData(t *testing.T) {
	dir := newTempDataDir(t)
	ctx := context.Background()
	db, err := Open(ctx, dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	now := time.Now().Unix()
	// 插 cache_entries
	_, err = db.SQLDB.ExecContext(ctx, `
		INSERT INTO cache_entries (registry, repository, digest, media_type, size_bytes, storage_path, hit_count, last_access_at, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		"dockerhub", "library/nginx", "sha256:abc", "application/octet-stream",
		1024*1024, "v2/dockerhub/library/nginx/blobs/sha256:abc", 5, now, now)
	if err != nil {
		t.Fatalf("insert cache: %v", err)
	}

	// 插一些 request_logs
	for i := 0; i < 10; i++ {
		cached := 0
		if i%2 == 0 {
			cached = 1
		}
		db.InsertAccessLog(ctx, AccessLogRecord{
			CreatedAt: now - int64(i), // 最近 24h
			Method:    "GET",
			Path:      "/v2/library/nginx/blobs/sha256:abc",
			Status:    200,
			Cached:    cached == 1,
			Bytes:     100,
		})
	}

	s, err := db.DashboardSummary(ctx)
	if err != nil {
		t.Fatalf("DashboardSummary: %v", err)
	}
	if s.CacheEntries != 1 {
		t.Errorf("CacheEntries = %d, want 1", s.CacheEntries)
	}
	if s.CacheBytes != 1024*1024 {
		t.Errorf("CacheBytes = %d, want 1MB", s.CacheBytes)
	}
	if s.RequestCount24h != 10 {
		t.Errorf("RequestCount24h = %d, want 10", s.RequestCount24h)
	}
	if s.HitCount != 5 {
		t.Errorf("HitCount = %d, want 5", s.HitCount)
	}
	if s.MissCount != 5 {
		t.Errorf("MissCount = %d, want 5", s.MissCount)
	}
}

func TestListEnabledUpstreams(t *testing.T) {
	dir := newTempDataDir(t)
	ctx := context.Background()
	db, err := Open(ctx, dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	ups, err := db.ListEnabledUpstreams(ctx)
	if err != nil {
		t.Fatalf("ListEnabledUpstreams: %v", err)
	}
	if len(ups) != 1 {
		t.Fatalf("len = %d, want 1", len(ups))
	}
	u := ups[0]
	if u.Name != "dockerhub" {
		t.Errorf("name = %q", u.Name)
	}
	if u.UpstreamURL != "https://registry-1.docker.io" {
		t.Errorf("url = %q", u.UpstreamURL)
	}
	if !u.Enabled {
		t.Errorf("enabled = false")
	}
}
