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
	ID             int64  `json:"id"`
	CreatedAt      int64  `json:"createdAt"` // unix 秒
	Method         string `json:"method"`
	Path           string `json:"path"`
	Status         int    `json:"status"`
	DurationMs     int64  `json:"durationMs"`
	Cached         bool   `json:"cached"`
	Bypassed       bool   `json:"bypassed"`
	BypassedReason string `json:"-"` // 同 BypassReason；保留做兼容
	BypassReason   string `json:"bypassReason"` // PRD §9.6.4: size_limit / disk_low
	ClientIP       string `json:"clientIp"`
	Bytes          int64  `json:"bytes"`
	Error          string `json:"error"`
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
	// 同步 BypassedReason 字段（兼容旧 BypassedReason 命名）
	if rec.BypassReason == "" && rec.BypassedReason != "" {
		rec.BypassReason = rec.BypassedReason
	}
	// bool 兼容路径：Bypassed=true + BypassReason="" 视为 "bypassed"
	if rec.Bypassed && rec.BypassReason == "" {
		rec.BypassReason = "bypassed"
	}
	bypassed := 0
	if rec.BypassReason != "" {
		bypassed = 1
	}
	_, err := d.SQLDB.ExecContext(ctx, `
		INSERT INTO request_logs
		(created_at, method, path, status, duration_ms, cached, bypassed, bypass_reason, client_ip, bytes, error)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, ts, rec.Method, rec.Path, rec.Status, rec.DurationMs, cached, bypassed, rec.BypassReason, rec.ClientIP, rec.Bytes, rec.Error)
	if err != nil {
		return fmt.Errorf("storage: insert access log: %w", err)
	}
	return nil
}

// LogFilter 封装 GET /api/logs 的筛选条件。
type LogFilter struct {
	Status    int    // 0 = 不限；否则精确匹配（如 500）或 5xx 模式（StatusClass=5 → status >= 500 AND status < 600）
	StatusCls int    // 1-5 = 匹配 1xx-5xx；0 = 不限
	Method    string // 空 = 不限
	Path      string // 子串 LIKE %path%
	Cached    *bool  // nil = 不限；true = HIT；false = MISS
	Bypassed  *bool  // nil = 不限；true = 旁路
	ClientIP  string // 精确匹配
	StartAt   int64  // unix 秒，0 = 不限
	EndAt     int64  // unix 秒，0 = 不限
}

// ListAccessLogs 分页查询 request_logs（按时间倒序，支持筛选）。
func (d *DB) ListAccessLogs(ctx context.Context, page, pageSize int, filter LogFilter) ([]AccessLogRecord, int, error) {
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 200 {
		pageSize = 50
	}
	offset := (page - 1) * pageSize

	// 构建 WHERE 子句
	where, args := buildLogWhere(filter)

	// 总数
	var total int
	countSQL := "SELECT COUNT(*) FROM request_logs" + where
	if err := d.SQLDB.QueryRowContext(ctx, countSQL, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("storage: count logs: %w", err)
	}

	querySQL := `SELECT id, created_at, method, path, status, duration_ms, cached, bypassed, bypass_reason, client_ip, bytes, error
		FROM request_logs` + where + ` ORDER BY id DESC LIMIT ? OFFSET ?`
	rows, err := d.SQLDB.QueryContext(ctx, querySQL, append(args, pageSize, offset)...)
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
			&r.DurationMs, &cachedI, &bypassed, &r.BypassReason, &r.ClientIP, &r.Bytes, &r.Error); err != nil {
			return nil, 0, fmt.Errorf("storage: scan log: %w", err)
		}
		r.Cached = cachedI == 1
		r.Bypassed = bypassed == 1
		r.BypassedReason = r.BypassReason
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	return out, total, nil
}

// buildLogWhere 根据 filter 生成 WHERE 子句 + 参数。
func buildLogWhere(f LogFilter) (string, []any) {
	var clauses []string
	var args []any

	if f.StatusCls >= 1 && f.StatusCls <= 5 {
		lo := f.StatusCls * 100
		hi := lo + 100
		clauses = append(clauses, "status >= ? AND status < ?")
		args = append(args, lo, hi)
	} else if f.Status > 0 {
		clauses = append(clauses, "status = ?")
		args = append(args, f.Status)
	}
	if f.Method != "" {
		clauses = append(clauses, "method = ?")
		args = append(args, f.Method)
	}
	if f.Path != "" {
		clauses = append(clauses, "path LIKE ?")
		args = append(args, "%"+f.Path+"%")
	}
	if f.Cached != nil {
		if *f.Cached {
			clauses = append(clauses, "cached = 1")
		} else {
			clauses = append(clauses, "cached = 0")
		}
	}
	if f.Bypassed != nil {
		if *f.Bypassed {
			clauses = append(clauses, "bypassed = 1")
		} else {
			clauses = append(clauses, "bypassed = 0")
		}
	}
	if f.ClientIP != "" {
		clauses = append(clauses, "client_ip = ?")
		args = append(args, f.ClientIP)
	}
	if f.StartAt > 0 {
		clauses = append(clauses, "created_at >= ?")
		args = append(args, f.StartAt)
	}
	if f.EndAt > 0 {
		clauses = append(clauses, "created_at <= ?")
		args = append(args, f.EndAt)
	}

	if len(clauses) == 0 {
		return "", nil
	}
	return " WHERE " + joinClauses(clauses, " AND "), args
}

func joinClauses(parts []string, sep string) string {
	if len(parts) == 0 {
		return ""
	}
	s := parts[0]
	for _, p := range parts[1:] {
		s += sep + p
	}
	return s
}

// PurgeAccessLogs 删除 created_at < before 的日志，返回删除行数。
func (d *DB) PurgeAccessLogs(ctx context.Context, before int64) (int64, error) {
	res, err := d.SQLDB.ExecContext(ctx, `DELETE FROM request_logs WHERE created_at < ?`, before)
	if err != nil {
		return 0, fmt.Errorf("storage: purge logs: %w", err)
	}
	n, _ := res.RowsAffected()
	return n, nil
}

// CountAccessLogs 返回 request_logs 总行数。
func (d *DB) CountAccessLogs(ctx context.Context) (int, error) {
	var n int
	if err := d.SQLDB.QueryRowContext(ctx, `SELECT COUNT(*) FROM request_logs`).Scan(&n); err != nil {
		return 0, fmt.Errorf("storage: count logs: %w", err)
	}
	return n, nil
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

// Count24hStatus 统计 24h 内 status >= X 的请求数（给诊断中心用）。
func (d *DB) Count24hStatus(ctx context.Context, minStatus int) (int, error) {
	cutoff := time.Now().Add(-24 * time.Hour).Unix()
	var n int
	if err := d.SQLDB.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM request_logs WHERE created_at >= ? AND status >= ?`,
		cutoff, minStatus).Scan(&n); err != nil {
		return 0, fmt.Errorf("storage: count 24h status: %w", err)
	}
	return n, nil
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

// ErrNotFound 表示 DB 中找不到目标。
var ErrNotFound = errors.New("storage: not found")

// 兜底：保留 sql.ErrNoRows 用于 IsNotFound 类判断。
var _ = sql.ErrNoRows
