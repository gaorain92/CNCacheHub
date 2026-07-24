// Package storage: registry_upstreams 数据访问（PRD §9.2.2 + §9.7.3）。
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
//   - username: 上游认证用户名（明文，不算 secret）
//   - has_password / has_token: 是否设置了密码 / bearer token（**不返回明文**）
//   - PasswordEnc / TokenEnc: 加密的凭据，仅供 proxy 上游请求使用
type Registry struct {
	ID           int64  `json:"id"`
	Name         string `json:"name"`
	UpstreamURL  string `json:"upstreamUrl"`
	MirrorPath   string `json:"mirrorPath"`
	Enabled      bool   `json:"enabled"`
	CreatedAt    int64  `json:"createdAt"`
	Username     string `json:"username,omitempty"`  // §9.7.3 凭据 — 不算 secret
	HasPassword  bool   `json:"hasPassword,omitempty"` // 密码已设标记
	HasToken     bool   `json:"hasToken,omitempty"`    // token 已设标记
	// 加密字段（json 永远不导出；只在 storage 内部使用）
	PasswordEnc []byte `json:"-"`
	TokenEnc    []byte `json:"-"`
}

// listEnabledUpstreamsSQL 是内部 SQL（含 credentials 列）。
const listAllUpstreamsSQL = `SELECT id, name, upstream_url, mirror_path, enabled, created_at, username, password_enc, token_enc FROM registry_upstreams`

// listEnabledUpstreamsWhereSQL 是 ListEnabledUpstreams 用的查询。
const listEnabledUpstreamsWhereSQL = listAllUpstreamsSQL + ` WHERE enabled = 1 ORDER BY name ASC`

// listAllUpstreamsOrderSQL 是 ListAllUpstreams 用的查询。
const listAllUpstreamsOrderSQL = listAllUpstreamsSQL + ` ORDER BY name ASC`

func scanRegistry(scanner interface {
	Scan(dest ...any) error
}) (Registry, error) {
	var r Registry
	var en int
	var pwEnc, tkEnc []byte
	if err := scanner.Scan(&r.ID, &r.Name, &r.UpstreamURL, &r.MirrorPath, &en, &r.CreatedAt, &r.Username, &pwEnc, &tkEnc); err != nil {
		return r, err
	}
	r.Enabled = en == 1
	if pwEnc != nil {
		r.PasswordEnc = pwEnc
		r.HasPassword = true
	}
	if tkEnc != nil {
		r.TokenEnc = tkEnc
		r.HasToken = true
	}
	return r, nil
}

// ListEnabledUpstreams 列出所有启用的 upstream（按 name ASC）。
func (d *DB) ListEnabledUpstreams(ctx context.Context) ([]Registry, error) {
	rows, err := d.SQLDB.QueryContext(ctx, listEnabledUpstreamsWhereSQL)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Registry
	for rows.Next() {
		r, err := scanRegistry(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// ListAllUpstreams 列出所有（启用 + 禁用），管理 UI 用。
func (d *DB) ListAllUpstreams(ctx context.Context) ([]Registry, error) {
	rows, err := d.SQLDB.QueryContext(ctx, listAllUpstreamsOrderSQL)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Registry
	for rows.Next() {
		r, err := scanRegistry(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// GetUpstreamByName 按 name 查。
func (d *DB) GetUpstreamByName(ctx context.Context, name string) (*Registry, error) {
	row := d.SQLDB.QueryRowContext(ctx, listAllUpstreamsSQL+` WHERE name = ?`, name)
	r, err := scanRegistry(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
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

// SetUpstreamCredentials 写上游凭据（§9.7.3）。
//
// username/password/token 全部 nil/空 = 不动对应列（保持原值）。
// 传 nilPtr=true 表示"清空该字段"（用户主动 unset）。
func (d *DB) SetUpstreamCredentials(ctx context.Context, name string, patch RegistryCredentialsPatch) error {
	sets := []string{}
	args := []any{}
	if patch.Username != nil {
		sets = append(sets, "username = ?")
		args = append(args, *patch.Username)
	}
	if patch.ClearPassword || patch.Password != nil {
		if patch.ClearPassword {
			sets = append(sets, "password_enc = NULL")
		} else {
			sets = append(sets, "password_enc = ?")
			args = append(args, *patch.Password)
		}
	}
	if patch.ClearToken || patch.Token != nil {
		if patch.ClearToken {
			sets = append(sets, "token_enc = NULL")
		} else {
			sets = append(sets, "token_enc = ?")
			args = append(args, *patch.Token)
		}
	}
	if len(sets) == 0 {
		return nil // no-op
	}
	q := "UPDATE registry_upstreams SET "
	for i, s := range sets {
		if i > 0 {
			q += ", "
		}
		q += s
	}
	q += " WHERE name = ?"
	args = append(args, name)
	res, err := d.SQLDB.ExecContext(ctx, q, args...)
	if err != nil {
		return fmt.Errorf("storage: set upstream credentials: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// RegistryCredentialsPatch 是 SetUpstreamCredentials 的入参。
//
// nil 指针 = 不动；空字符串 = 写入空（罕见）；ClearPassword/ClearToken = 显式清空。
type RegistryCredentialsPatch struct {
	Username      *string
	Password      *[]byte // 已经是密文（调用方用 master key 加密过的）
	Token         *[]byte
	ClearPassword bool
	ClearToken    bool
}
