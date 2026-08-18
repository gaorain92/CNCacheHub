// Package storage: system_settings 数据访问。
package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
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
	// Hugging Face 模型下载（PRD §9.4.5 扩展）
	// 用于 huggingface_models 类型 resource rule 的 Authorization: Bearer 注入
	SettingHuggingFaceToken = "huggingface_token"
	// 代理访问控制（P2#4 / PRD §9.7.2）
	SettingAccessControlEnabled        = "access_control_enabled"         // "true"/"false"
	SettingAccessControlToken          = "access_control_token"           // 任意字符串（启用时为空就视为 disabled）
	SettingAccessControlIPWhitelist    = "access_control_ip_whitelist"    // 逗号分隔 CIDR（例 "10.0.0.0/8,192.168.0.0/16"）
	SettingAccessControlLoopbackBypass = "access_control_loopback_bypass" // "true"/"false"，127/8 永远放行（默认 true）
	// 公开 Base URL（让 admin 配客户端可访问的 CNCH 地址）
	// 用于生成 client config（daemon.json / hosts.toml / k3s / verify.sh 等）。
	// 留空时 fallback 到 r.Host（nginx 直连场景）。
	SettingPublicBaseURL = "public_base_url"
	// 日志保留天数（0 = 不自动清理，默认 30 天）。
	SettingLogRetentionDays = "log_retention_days"
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

// GetMany 批量读（不存在返回空字符串）。
// 用于 access control 一次读 4 个 key。
func (d *DB) GetMany(ctx context.Context, keys ...string) (map[string]string, error) {
	out := make(map[string]string, len(keys))
	if len(keys) == 0 {
		return out, nil
	}
	// 构造 IN (?, ?, ?) — 用 placeholder 避免注入
	placeholders := make([]string, len(keys))
	args := make([]any, len(keys))
	for i, k := range keys {
		placeholders[i] = "?"
		args[i] = k
	}
	q := `SELECT key, value FROM system_settings WHERE key IN (` + strings.Join(placeholders, ",") + `)`
	rows, err := d.SQLDB.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	// 先把空值放进去（保证返回的 map 包含所有请求的 key）
	for _, k := range keys {
		out[k] = ""
	}
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return nil, err
		}
		out[k] = v
	}
	return out, rows.Err()
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
