// Package storage: system_settings 数据访问。
package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// Setting 是单条 system_settings 行。
type Setting struct {
	Key       string `json:"key"`
	Value     string `json:"value"`
	UpdatedAt int64  `json:"updatedAt"`
	UpdatedBy int64  `json:"updatedBy"`
}

// 系统设置 key 常量（避免散落字符串）。
const (
	SettingSmallVPSOpt      = "small_vps_opt"        // "true" / "false"
	SettingReserveSpaceGB   = "reserve_space_gb"     // 数字
	SettingMaxObjectSizeMB  = "max_object_size_mb"   // 数字
	SettingCacheTotalGB     = "cache_total_gb"       // 数字
	SettingCleanupTriggerPct = "cleanup_trigger_pct" // 数字（百分比 0-100）
	SettingCleanupTargetPct  = "cleanup_target_pct"  // 数字（百分比 0-100）
)

// GetSetting 按 key 查。
func (d *DB) GetSetting(ctx context.Context, key string) (*Setting, error) {
	row := d.SQLDB.QueryRowContext(ctx, `
		SELECT key, value, updated_at, updated_by FROM system_settings WHERE key = ?
	`, key)
	var s Setting
	if err := row.Scan(&s.Key, &s.Value, &s.UpdatedAt, &s.UpdatedBy); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &s, nil
}

// ListSettings 列出所有。
func (d *DB) ListSettings(ctx context.Context) ([]Setting, error) {
	rows, err := d.SQLDB.QueryContext(ctx, `
		SELECT key, value, updated_at, updated_by FROM system_settings ORDER BY key
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Setting
	for rows.Next() {
		var s Setting
		if err := rows.Scan(&s.Key, &s.Value, &s.UpdatedAt, &s.UpdatedBy); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// SetSetting 写一条（upsert）。
func (d *DB) SetSetting(ctx context.Context, key, value string, updatedBy int64) error {
	_, err := d.SQLDB.ExecContext(ctx, `
		INSERT INTO system_settings (key, value, updated_at, updated_by)
		VALUES (?, ?, ?, ?)
		ON CONFLICT (key) DO UPDATE SET
			value = excluded.value,
			updated_at = excluded.updated_at,
			updated_by = excluded.updated_by
	`, key, value, time.Now().Unix(), updatedBy)
	return err
}

// GetString 简化 helper：key → value（不存在返回 fallback）。
func (d *DB) GetString(ctx context.Context, key, fallback string) string {
	s, err := d.GetSetting(ctx, key)
	if err != nil {
		return fallback
	}
	return s.Value
}

// SetMany 批量写。
func (d *DB) SetMany(ctx context.Context, kvs map[string]string, updatedBy int64) error {
	tx, err := d.SQLDB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	now := time.Now().Unix()
	stmt, err := tx.PrepareContext(ctx, `
		INSERT INTO system_settings (key, value, updated_at, updated_by)
		VALUES (?, ?, ?, ?)
		ON CONFLICT (key) DO UPDATE SET
			value = excluded.value,
			updated_at = excluded.updated_at,
			updated_by = excluded.updated_by
	`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for k, v := range kvs {
		if _, err := stmt.ExecContext(ctx, k, v, now, updatedBy); err != nil {
			return fmt.Errorf("storage: set %s: %w", k, err)
		}
	}
	return tx.Commit()
}
