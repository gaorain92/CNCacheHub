package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// SteamAppID 是 steam_appids 行的 Go 表示（PRD §9.3.3）。
type SteamAppID struct {
	ID                      int64  `json:"id"`
	AppID                   int64  `json:"appId"`
	Name                    string `json:"name"`
	LoginType               string `json:"loginType"` // 'anonymous' | 'account'
	InstallDir              string `json:"installDir"`
	Enabled                 bool   `json:"enabled"`
	LastPreheatAt           int64  `json:"lastPreheatAt"`           // unix 秒；0 = 从未
	LastPreheatStatus       string `json:"lastPreheatStatus"`      // 'ok' | 'error' | 'running' | ''
	LastPreheatMessage      string `json:"lastPreheatMessage"`
	LastPreheatDurationMs   int64  `json:"lastPreheatDurationMs"`
	CacheBytesEstimate      int64  `json:"cacheBytesEstimate"`
	HitCount                int64  `json:"hitCount"`
	MissCount               int64  `json:"missCount"`
	CreatedAt               int64  `json:"createdAt"`
	UpdatedAt               int64  `json:"updatedAt"`
}

// ErrNotFound 已在 access_log.go 定义；这里复用。
var _ = errors.New

// ListSteamAppIDs 列出所有（按 app_id 升序）。
func (d *DB) ListSteamAppIDs(ctx context.Context) ([]SteamAppID, error) {
	rows, err := d.SQLDB.QueryContext(ctx, `
		SELECT id, app_id, name, login_type, install_dir, enabled,
		       last_preheat_at, last_preheat_status, last_preheat_message, last_preheat_duration_ms,
		       cache_bytes_estimate, hit_count, miss_count, created_at, updated_at
		FROM steam_appids
		ORDER BY app_id ASC
	`)
	if err != nil {
		return nil, fmt.Errorf("storage: list steam appids: %w", err)
	}
	defer rows.Close()
	out := make([]SteamAppID, 0, 16)
	for rows.Next() {
		var a SteamAppID
		var enabledI int
		if err := rows.Scan(&a.ID, &a.AppID, &a.Name, &a.LoginType, &a.InstallDir, &enabledI,
			&a.LastPreheatAt, &a.LastPreheatStatus, &a.LastPreheatMessage, &a.LastPreheatDurationMs,
			&a.CacheBytesEstimate, &a.HitCount, &a.MissCount, &a.CreatedAt, &a.UpdatedAt); err != nil {
			return nil, fmt.Errorf("storage: scan steam appid: %w", err)
		}
		a.Enabled = enabledI == 1
		out = append(out, a)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// GetSteamAppID 按主键取单条。
func (d *DB) GetSteamAppID(ctx context.Context, id int64) (SteamAppID, error) {
	row := d.SQLDB.QueryRowContext(ctx, `
		SELECT id, app_id, name, login_type, install_dir, enabled,
		       last_preheat_at, last_preheat_status, last_preheat_message, last_preheat_duration_ms,
		       cache_bytes_estimate, hit_count, miss_count, created_at, updated_at
		FROM steam_appids WHERE id = ?
	`, id)
	var a SteamAppID
	var enabledI int
	if err := row.Scan(&a.ID, &a.AppID, &a.Name, &a.LoginType, &a.InstallDir, &enabledI,
		&a.LastPreheatAt, &a.LastPreheatStatus, &a.LastPreheatMessage, &a.LastPreheatDurationMs,
		&a.CacheBytesEstimate, &a.HitCount, &a.MissCount, &a.CreatedAt, &a.UpdatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return SteamAppID{}, ErrNotFound
		}
		return SteamAppID{}, fmt.Errorf("storage: get steam appid: %w", err)
	}
	a.Enabled = enabledI == 1
	return a, nil
}

// CreateSteamAppID 新增一条；返回新 id。
func (d *DB) CreateSteamAppID(ctx context.Context, in SteamAppID) (SteamAppID, error) {
	now := time.Now().Unix()
	in.CreatedAt = now
	in.UpdatedAt = now
	if in.LoginType == "" {
		in.LoginType = "anonymous"
	}
	enabledI := 0
	if in.Enabled {
		enabledI = 1
	}
	res, err := d.SQLDB.ExecContext(ctx, `
		INSERT INTO steam_appids
		  (app_id, name, login_type, install_dir, enabled, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`, in.AppID, in.Name, in.LoginType, in.InstallDir, enabledI, in.CreatedAt, in.UpdatedAt)
	if err != nil {
		return SteamAppID{}, fmt.Errorf("storage: create steam appid: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return SteamAppID{}, fmt.Errorf("storage: last insert id: %w", err)
	}
	in.ID = id
	in.Enabled = enabledI == 1
	return in, nil
}

// UpdateSteamAppID 部分更新（除 app_id 外的字段）。
func (d *DB) UpdateSteamAppID(ctx context.Context, id int64, patch SteamAppIDPatch) (SteamAppID, error) {
	cur, err := d.GetSteamAppID(ctx, id)
	if err != nil {
		return SteamAppID{}, err
	}
	if patch.Name != nil {
		cur.Name = *patch.Name
	}
	if patch.LoginType != nil {
		cur.LoginType = *patch.LoginType
	}
	if patch.InstallDir != nil {
		cur.InstallDir = *patch.InstallDir
	}
	if patch.Enabled != nil {
		cur.Enabled = *patch.Enabled
	}
	if patch.CacheBytesEstimate != nil {
		cur.CacheBytesEstimate = *patch.CacheBytesEstimate
	}
	cur.UpdatedAt = time.Now().Unix()
	enabledI := 0
	if cur.Enabled {
		enabledI = 1
	}
	_, err = d.SQLDB.ExecContext(ctx, `
		UPDATE steam_appids
		SET name=?, login_type=?, install_dir=?, enabled=?, cache_bytes_estimate=?, updated_at=?
		WHERE id=?
	`, cur.Name, cur.LoginType, cur.InstallDir, enabledI, cur.CacheBytesEstimate, cur.UpdatedAt, id)
	if err != nil {
		return SteamAppID{}, fmt.Errorf("storage: update steam appid: %w", err)
	}
	return cur, nil
}

// DeleteSteamAppID 删一条。
func (d *DB) DeleteSteamAppID(ctx context.Context, id int64) error {
	_, err := d.SQLDB.ExecContext(ctx, `DELETE FROM steam_appids WHERE id=?`, id)
	if err != nil {
		return fmt.Errorf("storage: delete steam appid: %w", err)
	}
	return nil
}

// RecordPreheatResult 写预热结果到 row。
func (d *DB) RecordPreheatResult(ctx context.Context, id int64, status, message string, durationMs int64) error {
	now := time.Now().Unix()
	_, err := d.SQLDB.ExecContext(ctx, `
		UPDATE steam_appids
		SET last_preheat_at=?, last_preheat_status=?, last_preheat_message=?, last_preheat_duration_ms=?, updated_at=?
		WHERE id=?
	`, now, status, message, durationMs, now, id)
	if err != nil {
		return fmt.Errorf("storage: record preheat result: %w", err)
	}
	return nil
}

// SteamAppIDPatch 是 UpdateSteamAppID 的部分更新载体。
type SteamAppIDPatch struct {
	Name               *string
	LoginType          *string
	InstallDir         *string
	Enabled            *bool
	CacheBytesEstimate *int64
}
