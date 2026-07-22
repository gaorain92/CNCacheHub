package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// DNSConfig 是 dns_config 行的 Go 表示（与 dnsserver.Config 同形但解耦）。
type DNSConfig struct {
	ID          int64    `json:"id"`
	Enabled     bool     `json:"enabled"`
	ListenAddr  string   `json:"listenAddr"`
	Upstream    string   `json:"upstream"`
	AnswerIP    string   `json:"answerIp"`
	DomainRules []string `json:"domainRules"`
	CreatedAt   int64    `json:"createdAt"`
	UpdatedAt   int64    `json:"updatedAt"`
}

// GetDNSConfig 返回单行配置（无行返回 ErrNotFound）。
func (d *DB) GetDNSConfig(ctx context.Context) (DNSConfig, error) {
	row := d.SQLDB.QueryRowContext(ctx, `
		SELECT id, enabled, listen_addr, upstream, answer_ip, domain_rules, created_at, updated_at
		FROM dns_config
		WHERE id = 1
	`)
	var (
		c         DNSConfig
		enabledI  int
		rulesJSON string
	)
	if err := row.Scan(&c.ID, &enabledI, &c.ListenAddr, &c.Upstream, &c.AnswerIP, &rulesJSON, &c.CreatedAt, &c.UpdatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return DNSConfig{}, ErrNotFound
		}
		return DNSConfig{}, fmt.Errorf("storage: get dns config: %w", err)
	}
	c.Enabled = enabledI == 1
	if err := json.Unmarshal([]byte(rulesJSON), &c.DomainRules); err != nil {
		return DNSConfig{}, fmt.Errorf("storage: parse dns domain_rules: %w", err)
	}
	if c.DomainRules == nil {
		c.DomainRules = []string{}
	}
	return c, nil
}

// UpdateDNSConfig 更新配置（部分字段可选，零值不更新）。
// rules 为 nil 表示不改 rules；空切片表示清空。
func (d *DB) UpdateDNSConfig(ctx context.Context, patch DNSConfigPatch) (DNSConfig, error) {
	cur, err := d.GetDNSConfig(ctx)
	if err != nil {
		return DNSConfig{}, err
	}
	if patch.Enabled != nil {
		cur.Enabled = *patch.Enabled
	}
	if patch.ListenAddr != nil {
		cur.ListenAddr = *patch.ListenAddr
	}
	if patch.Upstream != nil {
		cur.Upstream = *patch.Upstream
	}
	if patch.AnswerIP != nil {
		cur.AnswerIP = *patch.AnswerIP
	}
	if patch.DomainRules != nil {
		cur.DomainRules = *patch.DomainRules
	}
	cur.UpdatedAt = time.Now().Unix()

	rulesJSON, err := json.Marshal(cur.DomainRules)
	if err != nil {
		return DNSConfig{}, fmt.Errorf("storage: marshal dns domain_rules: %w", err)
	}
	enabledI := 0
	if cur.Enabled {
		enabledI = 1
	}
	_, err = d.SQLDB.ExecContext(ctx, `
		UPDATE dns_config
		SET enabled = ?, listen_addr = ?, upstream = ?, answer_ip = ?, domain_rules = ?, updated_at = ?
		WHERE id = 1
	`, enabledI, cur.ListenAddr, cur.Upstream, cur.AnswerIP, string(rulesJSON), cur.UpdatedAt)
	if err != nil {
		return DNSConfig{}, fmt.Errorf("storage: update dns config: %w", err)
	}
	return cur, nil
}

// DNSConfigPatch 是 UpdateDNSConfig 的部分更新载体。
type DNSConfigPatch struct {
	Enabled     *bool
	ListenAddr  *string
	Upstream    *string
	AnswerIP    *string
	DomainRules *[]string
}
