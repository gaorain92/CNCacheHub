// Package storage: users / sessions / audit_logs 数据访问 + bcrypt 工具。
package storage

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"time"

	"golang.org/x/crypto/bcrypt"
)

// User 是 users 行的 Go 表示。
type User struct {
	ID                 int64  `json:"id"`
	Username           string `json:"username"`
	PasswordHash       string `json:"-"` // 永远不外露
	IsAdmin            bool   `json:"isAdmin"`
	MustChangePassword bool   `json:"mustChangePassword"`
	CreatedAt          int64  `json:"createdAt"`
	LastLoginAt        int64  `json:"lastLoginAt"`
	Disabled           bool   `json:"disabled"`
}

// Session 是 sessions 行的 Go 表示。
type Session struct {
	Token      string `json:"token"`
	UserID     int64  `json:"userId"`
	CreatedAt  int64  `json:"createdAt"`
	ExpiresAt  int64  `json:"expiresAt"`
	LastSeenAt int64  `json:"lastSeenAt"`
	IP         string `json:"ip"`
	UserAgent  string `json:"userAgent"`
}

// AuditLog 是 audit_logs 行的 Go 表示。
type AuditLog struct {
	ID        int64  `json:"id"`
	UserID    int64  `json:"userId"` // 0 表示匿名
	Action    string `json:"action"`
	Resource  string `json:"resource"`
	IP        string `json:"ip"`
	UserAgent string `json:"userAgent"`
	Status    string `json:"status"` // "ok" / "fail"
	Details   string `json:"details"`
	CreatedAt int64  `json:"createdAt"`
}

// BcryptCost 是 bcrypt 计算成本。生产 12；测试时调低加速。
const BcryptCost = 12

// HashPassword 用 bcrypt 哈希明文密码。
func HashPassword(plain string) (string, error) {
	if plain == "" {
		return "", errors.New("storage: empty password")
	}
	b, err := bcrypt.GenerateFromPassword([]byte(plain), BcryptCost)
	if err != nil {
		return "", fmt.Errorf("storage: hash password: %w", err)
	}
	return string(b), nil
}

// VerifyPassword 比对明文 vs bcrypt hash。
func VerifyPassword(hash, plain string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(plain)) == nil
}

// CreateUser 创建用户（默认非 admin）。
//
// 首次初始化时设 isAdmin=true；后续业务可调 PromoteToAdmin。
func (d *DB) CreateUser(ctx context.Context, username, plainPassword string, isAdmin bool) (*User, error) {
	hash, err := HashPassword(plainPassword)
	if err != nil {
		return nil, err
	}
	admin := 0
	if isAdmin {
		admin = 1
	}
	now := time.Now().Unix()
	res, err := d.SQLDB.ExecContext(ctx, `
		INSERT INTO users (username, password_hash, is_admin, must_change_password, created_at, last_login_at, disabled)
		VALUES (?, ?, ?, 0, ?, 0, 0)
	`, username, hash, admin, now)
	if err != nil {
		return nil, fmt.Errorf("storage: create user: %w", err)
	}
	id, _ := res.LastInsertId()
	return &User{
		ID:           id,
		Username:     username,
		PasswordHash: hash,
		IsAdmin:      isAdmin,
		CreatedAt:    now,
	}, nil
}

// GetUserByUsername 按 username 查询。
func (d *DB) GetUserByUsername(ctx context.Context, username string) (*User, error) {
	row := d.SQLDB.QueryRowContext(ctx, `
		SELECT id, username, password_hash, is_admin, must_change_password, created_at, last_login_at, disabled
		FROM users WHERE username = ?
	`, username)
	return scanUser(row)
}

// GetUserByID 按 id 查询。
func (d *DB) GetUserByID(ctx context.Context, id int64) (*User, error) {
	row := d.SQLDB.QueryRowContext(ctx, `
		SELECT id, username, password_hash, is_admin, must_change_password, created_at, last_login_at, disabled
		FROM users WHERE id = ?
	`, id)
	return scanUser(row)
}

// CountUsers 统计用户数（首次启动判定用）。
func (d *DB) CountUsers(ctx context.Context) (int, error) {
	var n int
	err := d.SQLDB.QueryRowContext(ctx, `SELECT COUNT(*) FROM users`).Scan(&n)
	return n, err
}

// UpdateUserPassword 改密。
func (d *DB) UpdateUserPassword(ctx context.Context, id int64, newPlain string) error {
	hash, err := HashPassword(newPlain)
	if err != nil {
		return err
	}
	_, err = d.SQLDB.ExecContext(ctx, `
		UPDATE users SET password_hash = ?, must_change_password = 0 WHERE id = ?
	`, hash, id)
	return err
}

// UpdateUserLastLogin 写 last_login_at。
func (d *DB) UpdateUserLastLogin(ctx context.Context, id int64) error {
	_, err := d.SQLDB.ExecContext(ctx, `UPDATE users SET last_login_at = ? WHERE id = ?`, time.Now().Unix(), id)
	return err
}

// PromoteToAdmin 设管理员。
func (d *DB) PromoteToAdmin(ctx context.Context, id int64) error {
	_, err := d.SQLDB.ExecContext(ctx, `UPDATE users SET is_admin = 1 WHERE id = ?`, id)
	return err
}

// scanUser 扫一行 users。
func scanUser(row *sql.Row) (*User, error) {
	var u User
	var admin, must, disabled int
	if err := row.Scan(&u.ID, &u.Username, &u.PasswordHash, &admin, &must, &u.CreatedAt, &u.LastLoginAt, &disabled); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	u.IsAdmin = admin == 1
	u.MustChangePassword = must == 1
	u.Disabled = disabled == 1
	return &u, nil
}

// ============================================================================
// Sessions
// ============================================================================

// CreateSession 签发新 session。
//
// token 长度 32 字节 → 43 字符 base64url（无 padding）。
// 默认过期时间 7 天（未来可配）。
func (d *DB) CreateSession(ctx context.Context, userID int64, ip, userAgent string, ttl time.Duration) (*Session, error) {
	if ttl <= 0 {
		ttl = 7 * 24 * time.Hour
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return nil, fmt.Errorf("storage: rand session token: %w", err)
	}
	token := base64.RawURLEncoding.EncodeToString(raw)
	now := time.Now().Unix()
	exp := now + int64(ttl.Seconds())
	if _, err := d.SQLDB.ExecContext(ctx, `
		INSERT INTO sessions (token, user_id, created_at, expires_at, last_seen_at, ip, user_agent)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`, token, userID, now, exp, now, ip, userAgent); err != nil {
		return nil, fmt.Errorf("storage: create session: %w", err)
	}
	return &Session{
		Token:      token,
		UserID:     userID,
		CreatedAt:  now,
		ExpiresAt:  exp,
		LastSeenAt: now,
		IP:         ip,
		UserAgent:  userAgent,
	}, nil
}

// GetSession 按 token 查（带过期检查 + 失效删除）。
//
// 行为：
//   - token 不存在 → ErrNotFound
//   - 已过期 → 删除该行 + 返回 ErrNotFound
func (d *DB) GetSession(ctx context.Context, token string) (*Session, error) {
	row := d.SQLDB.QueryRowContext(ctx, `
		SELECT token, user_id, created_at, expires_at, last_seen_at, ip, user_agent
		FROM sessions WHERE token = ?
	`, token)
	var s Session
	if err := row.Scan(&s.Token, &s.UserID, &s.CreatedAt, &s.ExpiresAt, &s.LastSeenAt, &s.IP, &s.UserAgent); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	if s.ExpiresAt < time.Now().Unix() {
		_ = d.DeleteSession(ctx, token)
		return nil, ErrNotFound
	}
	return &s, nil
}

// TouchSession 续期 last_seen_at（可选）。
func (d *DB) TouchSession(ctx context.Context, token string) error {
	_, err := d.SQLDB.ExecContext(ctx, `UPDATE sessions SET last_seen_at = ? WHERE token = ?`, time.Now().Unix(), token)
	return err
}

// DeleteSession 删一个 session（logout）。
func (d *DB) DeleteSession(ctx context.Context, token string) error {
	_, err := d.SQLDB.ExecContext(ctx, `DELETE FROM sessions WHERE token = ?`, token)
	return err
}

// DeleteUserSessions 删某用户所有 session（强制下线）。
func (d *DB) DeleteUserSessions(ctx context.Context, userID int64) error {
	_, err := d.SQLDB.ExecContext(ctx, `DELETE FROM sessions WHERE user_id = ?`, userID)
	return err
}

// PurgeExpiredSessions 清过期 session（cron 用）。
//
// 返回删除条数。
func (d *DB) PurgeExpiredSessions(ctx context.Context) (int64, error) {
	res, err := d.SQLDB.ExecContext(ctx, `DELETE FROM sessions WHERE expires_at < ?`, time.Now().Unix())
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// ============================================================================
// Audit logs
// ============================================================================

// WriteAudit 写一条审计日志。
func (d *DB) WriteAudit(ctx context.Context, e AuditLog) error {
	if e.CreatedAt == 0 {
		e.CreatedAt = time.Now().Unix()
	}
	_, err := d.SQLDB.ExecContext(ctx, `
		INSERT INTO audit_logs (user_id, action, resource, ip, user_agent, status, details, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
	`, e.UserID, e.Action, e.Resource, e.IP, e.UserAgent, e.Status, e.Details, e.CreatedAt)
	return err
}

// ListAuditLogs 分页查询审计日志（按时间倒序）。
func (d *DB) ListAuditLogs(ctx context.Context, page, pageSize int) ([]AuditLog, int, error) {
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 200 {
		pageSize = 50
	}
	var total int
	if err := d.SQLDB.QueryRowContext(ctx, `SELECT COUNT(*) FROM audit_logs`).Scan(&total); err != nil {
		return nil, 0, err
	}
	offset := (page - 1) * pageSize
	rows, err := d.SQLDB.QueryContext(ctx, `
		SELECT id, user_id, action, resource, ip, user_agent, status, details, created_at
		FROM audit_logs ORDER BY id DESC LIMIT ? OFFSET ?
	`, pageSize, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	var out []AuditLog
	for rows.Next() {
		var a AuditLog
		if err := rows.Scan(&a.ID, &a.UserID, &a.Action, &a.Resource, &a.IP, &a.UserAgent, &a.Status, &a.Details, &a.CreatedAt); err != nil {
			return nil, 0, err
		}
		out = append(out, a)
	}
	return out, total, rows.Err()
}
