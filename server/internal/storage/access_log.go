// Package storage: access log + dashboard summary 数据访问。
package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// AccessLogRecord 是单条 request_logs 行。
type AccessLogRecord struct {
	ID         int64
	CreatedAt  int64 // unix 秒
	Method     string
	Path       string
	Status     int
	DurationMs int64
	Cached     bool
	Bypassed   bool
	ClientIP   string
	Bytes      int64
	Error      string
}

// DashboardSummary 是仪表盘聚合数据。
type DashboardSummary struct {
	CacheEntries     int   `json:"cacheEntries"`
	CacheBytes       int64 `json:"cacheBytes"`
	CacheHits        int64 `json:"cacheHits"`        // 总命中次数（sum of hit_count）
	BypassedCount    int   `json:"bypassedCount"`   // 旁路条目数
	HitCount         int64 `json:"hitCount"`        // 24h 命中（request_logs）
	MissCount        int64 `json:"missCount"`       // 24h 未命中
	RequestCount24h  int   `json:"requestCount24h"`
	ErrorCount24h    int   `json:"errorCount24h"`
	BytesOut24h      int64 `json:"bytesOut24h"`
	ActiveUpstreams  int   `json:"activeUpstreams"`
	GeneratedAt      int64 `json:"generatedAt"`
}

// InsertAccessLog 写入一条 request_logs。
//
// 用 unix 秒时间戳 + INSERT；高频场景下应批量 INSERT（Phase 1.2 加）。
func (d *DB) InsertAccessLog(ctx context.Context, rec AccessLogRecord) error {
	ts := rec.CreatedAt
	if ts == 0 {
		ts = time.Now().Unix()
	}
	cached := 0
	if rec.Cached {
		cached = 1
	}
	bypassed := 0
	if rec.Bypassed {
		bypassed = 1
	}
	_, err := d.SQLDB.ExecContext(ctx, `
		INSERT INTO request_logs
		(created_at, method, path, status, duration_ms, cached, bypassed, client_ip, bytes, error)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, ts, rec.Method, rec.Path, rec.Status, rec.DurationMs, cached, bypassed, rec.ClientIP, rec.Bytes, rec.Error)
	if err != nil {
		return fmt.Errorf("storage: insert access log: %w", err)
	}
	return nil
}

// ListAccessLogs 分页查询 request_logs（按时间倒序）。
func (d *DB) ListAccessLogs(ctx context.Context, page, pageSize int) ([]AccessLogRecord, int, error) {
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 200 {
		pageSize = 50
	}
	offset := (page - 1) * pageSize

	// 总数
	var total int
	if err := d.SQLDB.QueryRowContext(ctx, `SELECT COUNT(*) FROM request_logs`).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("storage: count logs: %w", err)
	}

	rows, err := d.SQLDB.QueryContext(ctx, `
		SELECT id, created_at, method, path, status, duration_ms, cached, bypassed, client_ip, bytes, error
		FROM request_logs
		ORDER BY id DESC
		LIMIT ? OFFSET ?
	`, pageSize, offset)
	if err != nil {
		return nil, 0, fmt.Errorf("storage: query logs: %w", err)
	}
	defer rows.Close()

	var out []AccessLogRecord
	for rows.Next() {
		var (
			r        AccessLogRecord
			cachedI  int
			bypassed int
		)
		if err := rows.Scan(&r.ID, &r.CreatedAt, &r.Method, &r.Path, &r.Status,
			&r.DurationMs, &cachedI, &bypassed, &r.ClientIP, &r.Bytes, &r.Error); err != nil {
			return nil, 0, fmt.Errorf("storage: scan log: %w", err)
		}
		r.Cached = cachedI == 1
		r.Bypassed = bypassed == 1
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	return out, total, nil
}

// DashboardSummary 返回仪表盘聚合数据。
func (d *DB) DashboardSummary(ctx context.Context) (DashboardSummary, error) {
	var s DashboardSummary
	s.GeneratedAt = time.Now().Unix()
	cutoff := time.Now().Add(-24 * time.Hour).Unix()

	// 缓存聚合（来自 cache_entries）
	if err := d.SQLDB.QueryRowContext(ctx, `
		SELECT
			COUNT(*),
			COALESCE(SUM(size_bytes), 0),
			COALESCE(SUM(hit_count), 0),
			COALESCE(SUM(CASE WHEN bypassed = 1 THEN 1 ELSE 0 END), 0)
		FROM cache_entries
	`).Scan(&s.CacheEntries, &s.CacheBytes, &s.CacheHits, &s.BypassedCount); err != nil {
		return s, fmt.Errorf("storage: cache aggregate: %w", err)
	}

	// 24h 请求量 / 错误数 / 流量
	if err := d.SQLDB.QueryRowContext(ctx,
		`SELECT COUNT(*), COALESCE(SUM(CASE WHEN status >= 400 THEN 1 ELSE 0 END), 0), COALESCE(SUM(bytes), 0)
		 FROM request_logs WHERE created_at >= ?`, cutoff).Scan(&s.RequestCount24h, &s.ErrorCount24h, &s.BytesOut24h); err != nil {
		return s, fmt.Errorf("storage: 24h stats: %w", err)
	}

	// 24h 命中 / 未命中
	if err := d.SQLDB.QueryRowContext(ctx,
		`SELECT
			COALESCE(SUM(CASE WHEN cached = 1 THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN cached = 0 AND status = 200 AND bypassed = 0 THEN 1 ELSE 0 END), 0)
		 FROM request_logs WHERE created_at >= ? AND path LIKE '/v2/%'`, cutoff).Scan(&s.HitCount, &s.MissCount); err != nil {
		return s, fmt.Errorf("storage: 24h hit/miss: %w", err)
	}

	// 活跃 upstream 数
	if err := d.SQLDB.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM registry_upstreams WHERE enabled = 1`).Scan(&s.ActiveUpstreams); err != nil {
		return s, fmt.Errorf("storage: count upstreams: %w", err)
	}

	return s, nil
}

// RegistryUpstream 是 registry_upstreams 行的 Go 表示。
type RegistryUpstream struct {
	ID          int64  `json:"id"`
	Name        string `json:"name"`
	UpstreamURL string `json:"upstreamUrl"`
	MirrorPath  string `json:"mirrorPath"`
	Enabled     bool   `json:"enabled"`
	CreatedAt   int64  `json:"createdAt"`
}

// ListEnabledUpstreams 列出 enabled=1 的 upstream。
func (d *DB) ListEnabledUpstreams(ctx context.Context) ([]RegistryUpstream, error) {
	rows, err := d.SQLDB.QueryContext(ctx,
		`SELECT id, name, upstream_url, mirror_path, enabled, created_at
		 FROM registry_upstreams WHERE enabled = 1 ORDER BY name ASC`)
	if err != nil {
		return nil, fmt.Errorf("storage: query upstreams: %w", err)
	}
	defer rows.Close()
	var out []RegistryUpstream
	for rows.Next() {
		var u RegistryUpstream
		var enabledI int
		if err := rows.Scan(&u.ID, &u.Name, &u.UpstreamURL, &u.MirrorPath, &enabledI, &u.CreatedAt); err != nil {
			return nil, err
		}
		u.Enabled = enabledI == 1
		out = append(out, u)
	}
	return out, rows.Err()
}

// ErrNotFound 表示 DB 中找不到目标。
var ErrNotFound = errors.New("storage: not found")

// 兜底：保留 sql.ErrNoRows 用于 IsNotFound 类判断。
var _ = sql.ErrNoRows
