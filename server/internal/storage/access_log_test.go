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

// TestInsertAccessLog_BypassReason 回归测试：AccessLogRecord 的 BypassReason
// 必须正确写进 request_logs.bypass_reason。
//
// 历史 bug：存储层写 bypass 列时用 `if rec.Bypassed`（bool）但 proxy 传 BypassReason
// 是 string，导致 bypass 永远写 0；BypassReason 被 fallback 覆盖成 "bypassed"。
func TestInsertAccessLog_BypassReason(t *testing.T) {
	dir := newTempDataDir(t)
	ctx := context.Background()
	db, err := Open(ctx, dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	cases := []struct {
		name       string
		rec        AccessLogRecord
		wantStored int    // bypassed column value
		wantReason string // bypass_reason column value
	}{
		{"normal", AccessLogRecord{Method: "GET", Path: "/v2/", Status: 200, Cached: false, BypassReason: ""}, 0, ""},
		{"size_limit", AccessLogRecord{Method: "GET", Path: "/v2/lib/x/blobs/sha256:abc", Status: 200, BypassReason: "size_limit"}, 1, "size_limit"},
		{"disk_low", AccessLogRecord{Method: "GET", Path: "/v2/lib/x/blobs/sha256:def", Status: 200, BypassReason: "disk_low"}, 1, "disk_low"},
		{"bool_only", AccessLogRecord{Method: "GET", Path: "/v2/lib/x/blobs/sha256:ghi", Status: 200, Bypassed: true}, 1, "bypassed"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if err := db.InsertAccessLog(ctx, c.rec); err != nil {
				t.Fatalf("InsertAccessLog: %v", err)
			}
		})
	}

	rows, err := db.SQLDB.QueryContext(ctx, `SELECT bypassed, bypass_reason FROM request_logs ORDER BY id`)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	defer rows.Close()
	i := 0
	for rows.Next() {
		var bypassed int
		var reason string
		if err := rows.Scan(&bypassed, &reason); err != nil {
			t.Fatalf("scan: %v", err)
		}
		if bypassed != cases[i].wantStored {
			t.Errorf("case %d (%s): bypassed = %d, want %d", i, cases[i].name, bypassed, cases[i].wantStored)
		}
		if reason != cases[i].wantReason {
			t.Errorf("case %d (%s): bypass_reason = %q, want %q", i, cases[i].name, reason, cases[i].wantReason)
		}
		i++
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
