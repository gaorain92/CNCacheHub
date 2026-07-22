// Package storage: registry_upstreams 数据访问（PRD §9.2.2）。
package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// Registry 是 registry_upstreams 行的 Go 表示。
//
// 字段说明：
//   - name: 短代号，client 路径首段（"dockerhub"/"ghcr"/"quay"/"k8s"）
//   - upstream_url: 实际上游 registry 地址
//   - mirror_path: client 访问路径前缀；空表示默认 upstream（/v2/* 通配）
//   - enabled: 是否启用；0/1
type Registry struct {
	ID          int64  `json:"id"`
	Name        string `json:"name"`
	UpstreamURL string `json:"upstreamUrl"`
	MirrorPath  string `json:"mirrorPath"`
	Enabled     bool   `json:"enabled"`
	CreatedAt   int64  `json:"createdAt"`
}

// ListEnabledUpstreams 列出所有启用的 upstream（按 name ASC）。
func (d *DB) ListEnabledUpstreams(ctx context.Context) ([]Registry, error) {
	rows, err := d.SQLDB.QueryContext(ctx, `
		SELECT id, name, upstream_url, mirror_path, enabled, created_at
		FROM registry_upstreams WHERE enabled = 1 ORDER BY name ASC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Registry
	for rows.Next() {
		var r Registry
		var en int
		if err := rows.Scan(&r.ID, &r.Name, &r.UpstreamURL, &r.MirrorPath, &en, &r.CreatedAt); err != nil {
			return nil, err
		}
		r.Enabled = en == 1
		out = append(out, r)
	}
	return out, rows.Err()
}

// ListAllUpstreams 列出所有（启用 + 禁用），管理 UI 用。
func (d *DB) ListAllUpstreams(ctx context.Context) ([]Registry, error) {
	rows, err := d.SQLDB.QueryContext(ctx, `
		SELECT id, name, upstream_url, mirror_path, enabled, created_at
		FROM registry_upstreams ORDER BY name ASC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Registry
	for rows.Next() {
		var r Registry
		var en int
		if err := rows.Scan(&r.ID, &r.Name, &r.UpstreamURL, &r.MirrorPath, &en, &r.CreatedAt); err != nil {
			return nil, err
		}
		r.Enabled = en == 1
		out = append(out, r)
	}
	return out, rows.Err()
}

// GetUpstreamByName 按 name 查。
func (d *DB) GetUpstreamByName(ctx context.Context, name string) (*Registry, error) {
	row := d.SQLDB.QueryRowContext(ctx, `
		SELECT id, name, upstream_url, mirror_path, enabled, created_at
		FROM registry_upstreams WHERE name = ?
	`, name)
	var r Registry
	var en int
	if err := row.Scan(&r.ID, &r.Name, &r.UpstreamURL, &r.MirrorPath, &en, &r.CreatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	r.Enabled = en == 1
	return &r, nil
}

// SetUpstreamEnabled 启停 upstream。
func (d *DB) SetUpstreamEnabled(ctx context.Context, name string, enabled bool) error {
	en := 0
	if enabled {
		en = 1
	}
	res, err := d.SQLDB.ExecContext(ctx, `
		UPDATE registry_upstreams SET enabled = ? WHERE name = ?
	`, en, name)
	if err != nil {
		return fmt.Errorf("storage: set upstream enabled: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}
