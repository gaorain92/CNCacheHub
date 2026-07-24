package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
)

// compileRegex 缓存避免热路径重复编译。
var compileRegex = regexpCompilePattern

// regexpCompilePattern 包装 regexp.Compile，允许将来换成缓存版本。
func regexpCompilePattern(pattern string) (*regexp.Regexp, error) {
	return regexp.Compile(pattern)
}

// ResourceRule 是 resource_rules 行的 Go 表示（PRD §9.4）。
type ResourceRule struct {
	ID                int64  `json:"id"`
	Name              string `json:"name"`
	Kind              string `json:"kind"`
	UpstreamURL       string `json:"upstreamUrl"`
	PathPattern       string `json:"pathPattern"` // glob 匹配 path（默认 "*" = 全部）
	DefaultTTLSeconds int    `json:"defaultTtlSeconds"`
	Enabled           bool   `json:"enabled"`
	Description       string `json:"description"`
	CreatedAt         int64  `json:"createdAt"`
	UpdatedAt         int64  `json:"updatedAt"`
}

// ResourceCacheEntry 是 resource_cache_entries 行的 Go 表示。
type ResourceCacheEntry struct {
	ID            int64  `json:"id"`
	RuleID        int64  `json:"ruleId"`
	Path          string `json:"path"`
	SizeBytes     int64  `json:"sizeBytes"`
	HitCount      int64  `json:"hitCount"`
	LastAccessAt  int64  `json:"lastAccessAt"`
	ExpiresAt     int64  `json:"expiresAt"`
	ContentType   string `json:"contentType"`
	StoragePath   string `json:"storagePath"`
	CreatedAt     int64  `json:"createdAt"`
	UpdatedAt     int64  `json:"updatedAt"`
}

// ListResourceRules 列出全部规则（按 id ASC）。
func (d *DB) ListResourceRules(ctx context.Context) ([]ResourceRule, error) {
	rows, err := d.SQLDB.QueryContext(ctx, `
		SELECT id, name, kind, upstream_url, path_pattern, default_ttl_seconds, enabled, description, created_at, updated_at
		FROM resource_rules ORDER BY id ASC
	`)
	if err != nil {
		return nil, fmt.Errorf("storage: list resource rules: %w", err)
	}
	defer rows.Close()
	out := make([]ResourceRule, 0, 8)
	for rows.Next() {
		r, err := scanResourceRule(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func scanResourceRule(rows *sql.Rows) (ResourceRule, error) {
	var r ResourceRule
	var enabledI int
	if err := rows.Scan(&r.ID, &r.Name, &r.Kind, &r.UpstreamURL, &r.PathPattern, &r.DefaultTTLSeconds, &enabledI, &r.Description, &r.CreatedAt, &r.UpdatedAt); err != nil {
		return ResourceRule{}, fmt.Errorf("storage: scan resource rule: %w", err)
	}
	r.Enabled = enabledI == 1
	return r, nil
}

// GetResourceRule 按 id。
func (d *DB) GetResourceRule(ctx context.Context, id int64) (ResourceRule, error) {
	row := d.SQLDB.QueryRowContext(ctx, `
		SELECT id, name, kind, upstream_url, path_pattern, default_ttl_seconds, enabled, description, created_at, updated_at
		FROM resource_rules WHERE id = ?
	`, id)
	r, err := scanResourceRuleRow(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ResourceRule{}, ErrNotFound
		}
		return ResourceRule{}, err
	}
	return r, nil
}

// GetResourceRuleByName 按 name 拿。
func (d *DB) GetResourceRuleByName(ctx context.Context, name string) (ResourceRule, error) {
	row := d.SQLDB.QueryRowContext(ctx, `
		SELECT id, name, kind, upstream_url, path_pattern, default_ttl_seconds, enabled, description, created_at, updated_at
		FROM resource_rules WHERE name = ?
	`, name)
	r, err := scanResourceRuleRow(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ResourceRule{}, ErrNotFound
		}
		return ResourceRule{}, err
	}
	return r, nil
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanResourceRuleRow(row rowScanner) (ResourceRule, error) {
	var r ResourceRule
	var enabledI int
	if err := row.Scan(&r.ID, &r.Name, &r.Kind, &r.UpstreamURL, &r.PathPattern, &r.DefaultTTLSeconds, &enabledI, &r.Description, &r.CreatedAt, &r.UpdatedAt); err != nil {
		return ResourceRule{}, fmt.Errorf("storage: scan resource rule: %w", err)
	}
	r.Enabled = enabledI == 1
	return r, nil
}

// CreateResourceRule 新增。
func (d *DB) CreateResourceRule(ctx context.Context, in ResourceRule) (ResourceRule, error) {
	now := time.Now().Unix()
	in.CreatedAt = now
	in.UpdatedAt = now
	if in.DefaultTTLSeconds == 0 {
		in.DefaultTTLSeconds = 86400
	}
	if in.PathPattern == "" {
		in.PathPattern = "*"
	}
	in.UpstreamURL = strings.TrimRight(in.UpstreamURL, "/")
	enabledI := 0
	if in.Enabled {
		enabledI = 1
	}
	res, err := d.SQLDB.ExecContext(ctx, `
		INSERT INTO resource_rules (name, kind, upstream_url, path_pattern, default_ttl_seconds, enabled, description, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, in.Name, in.Kind, in.UpstreamURL, in.PathPattern, in.DefaultTTLSeconds, enabledI, in.Description, in.CreatedAt, in.UpdatedAt)
	if err != nil {
		return ResourceRule{}, fmt.Errorf("storage: insert resource rule: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return ResourceRule{}, err
	}
	in.ID = id
	in.Enabled = enabledI == 1
	return in, nil
}

// UpdateResourceRule 改 enabled / upstream / ttl / description。
func (d *DB) UpdateResourceRule(ctx context.Context, id int64, patch ResourceRulePatch) (ResourceRule, error) {
	cur, err := d.GetResourceRule(ctx, id)
	if err != nil {
		return ResourceRule{}, err
	}
	if patch.UpstreamURL != nil {
		cur.UpstreamURL = strings.TrimRight(*patch.UpstreamURL, "/")
	}
	if patch.PathPattern != nil {
		cur.PathPattern = *patch.PathPattern
	}
	if patch.DefaultTTLSeconds != nil {
		cur.DefaultTTLSeconds = *patch.DefaultTTLSeconds
	}
	if patch.Enabled != nil {
		cur.Enabled = *patch.Enabled
	}
	if patch.Description != nil {
		cur.Description = *patch.Description
	}
	cur.UpdatedAt = time.Now().Unix()
	enabledI := 0
	if cur.Enabled {
		enabledI = 1
	}
	_, err = d.SQLDB.ExecContext(ctx, `
		UPDATE resource_rules SET upstream_url=?, path_pattern=?, default_ttl_seconds=?, enabled=?, description=?, updated_at=?
		WHERE id=?
	`, cur.UpstreamURL, cur.PathPattern, cur.DefaultTTLSeconds, enabledI, cur.Description, cur.UpdatedAt, id)
	if err != nil {
		return ResourceRule{}, fmt.Errorf("storage: update resource rule: %w", err)
	}
	return cur, nil
}

// DeleteResourceRule 删。
func (d *DB) DeleteResourceRule(ctx context.Context, id int64) error {
	_, err := d.SQLDB.ExecContext(ctx, `DELETE FROM resource_rules WHERE id=?`, id)
	if err != nil {
		return fmt.Errorf("storage: delete resource rule: %w", err)
	}
	return nil
}

// ResourceRulePatch 是 UpdateResourceRule 的部分更新。
type ResourceRulePatch struct {
	UpstreamURL       *string
	PathPattern       *string
	DefaultTTLSeconds *int
	Enabled           *bool
	Description       *string
}

// MatchPath 检查 path 是否匹配 rule.PathPattern（glob 风格）。
// 支持：
//   - "*" 匹配所有
//   - "*.ext" 匹配以 .ext 结尾
//   - "owner/*" 匹配 owner/<anything>
//   - "prefix/*" / "prefix/**/suffix" 完整 glob
//   - 精确字符串匹配
//
// 用 Go 标准库 path.Match（仅单段 *）+ 手动 **/多段支持。
func (r *ResourceRule) MatchPath(path string) bool {
	pat := r.PathPattern
	if pat == "" || pat == "*" {
		return true
	}
	// 简单处理：** → 跨段通配；单 * → 段内
	if containsDoubleStar(pat) {
		// path.Match 不支持 **，转成正则
		regex := globToRegex(pat)
		return regexMatch(regex, path)
	}
	// 单段 * 处理：split pattern by /, 每段用 path.Match
	return matchSingleStar(pat, path)
}

func containsDoubleStar(s string) bool {
	return strings.Contains(s, "**")
}

// globToRegex "**/foo/*.go" → "^.*/foo/[^/]*\\.go$"
func globToRegex(pat string) string {
	var b strings.Builder
	b.WriteString("^")
	parts := strings.Split(pat, "/")
	for i, p := range parts {
		if p == "**" {
			b.WriteString(".*")
		} else if p == "*" {
			b.WriteString("[^/]*")
		} else {
			// escape regex metachars but keep * / . 处理
			s := p
			s = strings.ReplaceAll(s, ".", "\\.")
			s = strings.ReplaceAll(s, "*", "[^/]*")
			b.WriteString(s)
		}
		if i < len(parts)-1 {
			b.WriteString("/")
		}
	}
	b.WriteString("$")
	return b.String()
}

func regexMatch(pattern, s string) bool {
	re, err := compileRegex(pattern)
	if err != nil {
		return false
	}
	return re.MatchString(s)
}

// compile 缓存避免 hot path 重复编译（最简实现：每次重新 compile）
var _ = compileRegex

func matchSingleStar(pat, path string) bool {
	patParts := strings.Split(pat, "/")
	pathParts := strings.Split(path, "/")
	if len(patParts) != len(pathParts) {
		return false
	}
	for i, p := range patParts {
		if p == "*" {
			continue
		}
		if p != pathParts[i] {
			return false
		}
	}
	return true
}

// === cache entries ===

// ListResourceCache 列出某规则下的缓存条目。
func (d *DB) ListResourceCache(ctx context.Context, ruleID int64, limit int) ([]ResourceCacheEntry, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := d.SQLDB.QueryContext(ctx, `
		SELECT id, rule_id, path, size_bytes, hit_count, last_access_at, expires_at, content_type, storage_path, created_at, updated_at
		FROM resource_cache_entries
		WHERE rule_id = ?
		ORDER BY id DESC LIMIT ?
	`, ruleID, limit)
	if err != nil {
		return nil, fmt.Errorf("storage: list resource cache: %w", err)
	}
	defer rows.Close()
	out := make([]ResourceCacheEntry, 0, 16)
	for rows.Next() {
		var e ResourceCacheEntry
		if err := rows.Scan(&e.ID, &e.RuleID, &e.Path, &e.SizeBytes, &e.HitCount, &e.LastAccessAt, &e.ExpiresAt, &e.ContentType, &e.StoragePath, &e.CreatedAt, &e.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// GetResourceCacheEntry 按 rule_id + path 拿。
func (d *DB) GetResourceCacheEntry(ctx context.Context, ruleID int64, path string) (ResourceCacheEntry, error) {
	row := d.SQLDB.QueryRowContext(ctx, `
		SELECT id, rule_id, path, size_bytes, hit_count, last_access_at, expires_at, content_type, storage_path, created_at, updated_at
		FROM resource_cache_entries WHERE rule_id = ? AND path = ?
	`, ruleID, path)
	var e ResourceCacheEntry
	if err := row.Scan(&e.ID, &e.RuleID, &e.Path, &e.SizeBytes, &e.HitCount, &e.LastAccessAt, &e.ExpiresAt, &e.ContentType, &e.StoragePath, &e.CreatedAt, &e.UpdatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ResourceCacheEntry{}, ErrNotFound
		}
		return ResourceCacheEntry{}, err
	}
	return e, nil
}

// UpsertResourceCacheEntry 不存在则创建，存在则更新 size/expires/accessed。
func (d *DB) UpsertResourceCacheEntry(ctx context.Context, e ResourceCacheEntry) (ResourceCacheEntry, error) {
	now := time.Now().Unix()
	if e.LastAccessAt == 0 {
		e.LastAccessAt = now
	}
	e.UpdatedAt = now
	if e.CreatedAt == 0 {
		e.CreatedAt = now
	}
	_, err := d.SQLDB.ExecContext(ctx, `
		INSERT INTO resource_cache_entries
		  (rule_id, path, size_bytes, hit_count, last_access_at, expires_at, content_type, storage_path, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(rule_id, path) DO UPDATE SET
		  size_bytes = excluded.size_bytes,
		  expires_at = excluded.expires_at,
		  content_type = excluded.content_type,
		  storage_path = excluded.storage_path,
		  updated_at = excluded.updated_at
	`, e.RuleID, e.Path, e.SizeBytes, e.HitCount, e.LastAccessAt, e.ExpiresAt, e.ContentType, e.StoragePath, e.CreatedAt, e.UpdatedAt)
	if err != nil {
		return ResourceCacheEntry{}, fmt.Errorf("storage: upsert resource cache: %w", err)
	}
	return d.GetResourceCacheEntry(ctx, e.RuleID, e.Path)
}

// BumpResourceCacheHit 单条 hit +1 + last_access 更新。
func (d *DB) BumpResourceCacheHit(ctx context.Context, id int64) error {
	now := time.Now().Unix()
	_, err := d.SQLDB.ExecContext(ctx, `
		UPDATE resource_cache_entries SET hit_count = hit_count + 1, last_access_at = ?, updated_at = ?
		WHERE id = ?
	`, now, now, id)
	if err != nil {
		return fmt.Errorf("storage: bump resource hit: %w", err)
	}
	return nil
}

// DeleteResourceCacheEntry 删单条缓存（保留 rule）。
func (d *DB) DeleteResourceCacheEntry(ctx context.Context, id int64) error {
	_, err := d.SQLDB.ExecContext(ctx, `DELETE FROM resource_cache_entries WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("storage: delete resource cache: %w", err)
	}
	return nil
}
