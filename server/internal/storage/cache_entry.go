// Package storage: cache_entries 数据访问。
package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// CacheEntry 是 cache_entries 行的 Go 表示。
type CacheEntry struct {
	ID            int64  `json:"id"`
	Registry      string `json:"registry"`
	Repository    string `json:"repository"`
	Digest        string `json:"digest"`
	MediaType     string `json:"mediaType"`
	SizeBytes     int64  `json:"sizeBytes"`
	StoragePath   string `json:"storagePath"`
	HitCount      int    `json:"hitCount"`
	LastAccessAt  int64  `json:"lastAccessAt"`
	CreatedAt     int64  `json:"createdAt"`
	Bypassed      bool   `json:"bypassed"`
	BypassReason  string `json:"bypassReason"`
}

// UpsertCacheEntry 插入或更新一条 cache_entries。
//
// 行为：
//   - 不存在：INSERT，hit_count=0
//   - 已存在：更新 size_bytes / media_type / storage_path / last_access_at；
//     hit_count 保留（命中次数应通过 TouchCacheEntry 累加）
//
// 返回 rowid。
func (d *DB) UpsertCacheEntry(ctx context.Context, e CacheEntry) (int64, error) {
	now := time.Now().Unix()
	if e.LastAccessAt == 0 {
		e.LastAccessAt = now
	}
	if e.CreatedAt == 0 {
		e.CreatedAt = now
	}
	bypassed := 0
	if e.Bypassed {
		bypassed = 1
	}
	res, err := d.SQLDB.ExecContext(ctx, `
		INSERT INTO cache_entries
		(registry, repository, digest, media_type, size_bytes, storage_path,
		 hit_count, last_access_at, created_at, bypassed, bypass_reason)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (registry, repository, digest) WHERE digest != ''
		DO UPDATE SET
			media_type = excluded.media_type,
			size_bytes = excluded.size_bytes,
			storage_path = excluded.storage_path,
			last_access_at = excluded.last_access_at,
			bypassed = excluded.bypassed,
			bypass_reason = excluded.bypass_reason
	`, e.Registry, e.Repository, e.Digest, e.MediaType, e.SizeBytes, e.StoragePath,
		e.HitCount, e.LastAccessAt, e.CreatedAt, bypassed, e.BypassReason)
	if err != nil {
		return 0, fmt.Errorf("storage: upsert cache entry: %w", err)
	}
	return res.LastInsertId()
}

// TouchCacheEntry 命中时调用：last_access_at = now(), hit_count += 1。
// 不存在时返回 nil（不报错，让首次落盘走 Upsert 路径）。
func (d *DB) TouchCacheEntry(ctx context.Context, registry, repo, digest string) error {
	now := time.Now().Unix()
	res, err := d.SQLDB.ExecContext(ctx, `
		UPDATE cache_entries
		SET last_access_at = ?,
		    hit_count = hit_count + 1
		WHERE registry = ? AND repository = ? AND digest = ?
	`, now, registry, repo, digest)
	if err != nil {
		return fmt.Errorf("storage: touch cache entry: %w", err)
	}
	n, _ := res.RowsAffected()
	_ = n // 不存在时 n=0；不报错
	return nil
}

// ListCacheEntries 分页查询 cache_entries。
//
// query 用于按 registry / repository 过滤（可选）。
func (d *DB) ListCacheEntries(ctx context.Context, page, pageSize int, query string) ([]CacheEntry, int, error) {
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 200 {
		pageSize = 20
	}
	offset := (page - 1) * pageSize

	where := ""
	args := []any{}
	if query != "" {
		// 简单 LIKE：匹配 repository / digest / registry 任一。
		// 用 escapeLike 转义用户输入里的 % _ \，避免搜索 "%" 匹配整表。
		where = "WHERE repository LIKE ? ESCAPE '\\' OR digest LIKE ? ESCAPE '\\' OR registry LIKE ? ESCAPE '\\'"
		like := likePattern(query, maxSearchQueryLen)
		args = append(args, like, like, like)
	}

	// 总数
	var total int
	countQuery := "SELECT COUNT(*) FROM cache_entries " + where
	if err := d.SQLDB.QueryRowContext(ctx, countQuery, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("storage: count cache: %w", err)
	}

	listQuery := `
		SELECT id, registry, repository, digest, media_type, size_bytes, storage_path,
		       hit_count, last_access_at, created_at, bypassed, bypass_reason
		FROM cache_entries ` + where + `
		ORDER BY last_access_at DESC
		LIMIT ? OFFSET ?`
	args = append(args, pageSize, offset)
	rows, err := d.SQLDB.QueryContext(ctx, listQuery, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("storage: query cache: %w", err)
	}
	defer rows.Close()

	var out []CacheEntry
	for rows.Next() {
		var e CacheEntry
		var bypassed int
		if err := rows.Scan(&e.ID, &e.Registry, &e.Repository, &e.Digest, &e.MediaType,
			&e.SizeBytes, &e.StoragePath, &e.HitCount, &e.LastAccessAt, &e.CreatedAt,
			&bypassed, &e.BypassReason); err != nil {
			return nil, 0, err
		}
		e.Bypassed = bypassed == 1
		out = append(out, e)
	}
	return out, total, rows.Err()
}

// GetCacheEntryByID 按 id 查（用于 DELETE 前取 storage_path）。
func (d *DB) GetCacheEntryByID(ctx context.Context, id int64) (*CacheEntry, error) {
	row := d.SQLDB.QueryRowContext(ctx, `
		SELECT id, registry, repository, digest, media_type, size_bytes, storage_path,
		       hit_count, last_access_at, created_at, bypassed, bypass_reason
		FROM cache_entries WHERE id = ?
	`, id)
	var e CacheEntry
	var bypassed int
	if err := row.Scan(&e.ID, &e.Registry, &e.Repository, &e.Digest, &e.MediaType,
		&e.SizeBytes, &e.StoragePath, &e.HitCount, &e.LastAccessAt, &e.CreatedAt,
		&bypassed, &e.BypassReason); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	e.Bypassed = bypassed == 1
	return &e, nil
}

// DeleteCacheEntry 按 id 删行。文件删除由 cache.Store 负责。
func (d *DB) DeleteCacheEntry(ctx context.Context, id int64) error {
	_, err := d.SQLDB.ExecContext(ctx, `DELETE FROM cache_entries WHERE id = ?`, id)
	return err
}

// CacheAggregateStats 缓存总览（用于仪表盘）。
type CacheAggregateStats struct {
	TotalEntries  int   `json:"totalEntries"`
	TotalBytes    int64 `json:"totalBytes"`
	TotalHits     int64 `json:"totalHits"`
	BypassedCount int   `json:"bypassedCount"`
}

// CacheAggregate 返回缓存总览。
func (d *DB) CacheAggregate(ctx context.Context) (CacheAggregateStats, error) {
	var s CacheAggregateStats
	row := d.SQLDB.QueryRowContext(ctx, `
		SELECT
			COUNT(*),
			COALESCE(SUM(size_bytes), 0),
			COALESCE(SUM(hit_count), 0),
			COALESCE(SUM(CASE WHEN bypassed = 1 THEN 1 ELSE 0 END), 0)
		FROM cache_entries
	`)
	if err := row.Scan(&s.TotalEntries, &s.TotalBytes, &s.TotalHits, &s.BypassedCount); err != nil {
		return s, fmt.Errorf("storage: cache aggregate: %w", err)
	}
	return s, nil
}
