package storage

import (
	"context"
	"testing"
	"time"
)

// TestOpen_AuthSchema 验证 0006 跑完表存在。
func TestOpen_AuthSchema(t *testing.T) {
	dir := newTempDataDir(t)
	ctx := context.Background()
	db, err := Open(ctx, dir)
	if err != nil {
		t.Fatalf("Open() error: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	for _, table := range []string{"users", "sessions", "audit_logs"} {
		var n int
		if err := db.SQLDB.QueryRowContext(ctx, `SELECT COUNT(*) FROM `+table).Scan(&n); err != nil {
			t.Errorf("query %s: %v", table, err)
		}
	}
}

// TestCreateUser_AndGetByUsername 验证 CreateUser + GetUserByUsername。
func TestCreateUser_AndGetByUsername(t *testing.T) {
	dir := newTempDataDir(t)
	ctx := context.Background()
	db, err := Open(ctx, dir)
	if err != nil {
		t.Fatalf("Open() error: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	u, err := db.CreateUser(ctx, "alice", "hunter2", true)
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	if u.ID <= 0 {
		t.Errorf("ID = %d, want > 0", u.ID)
	}
	if u.Username != "alice" {
		t.Errorf("Username = %q, want alice", u.Username)
	}
	if !u.IsAdmin {
		t.Errorf("IsAdmin = false, want true")
	}
	if u.PasswordHash == "" || u.PasswordHash == "hunter2" {
		t.Errorf("PasswordHash not hashed: %q", u.PasswordHash)
	}
	// 验证 hash 正确
	if !VerifyPassword(u.PasswordHash, "hunter2") {
		t.Errorf("VerifyPassword(hunter2) = false, want true")
	}
	if VerifyPassword(u.PasswordHash, "wrong") {
		t.Errorf("VerifyPassword(wrong) = true, want false")
	}

	got, err := db.GetUserByUsername(ctx, "alice")
	if err != nil {
		t.Fatalf("GetUserByUsername: %v", err)
	}
	if got.ID != u.ID {
		t.Errorf("ID = %d, want %d", got.ID, u.ID)
	}

	// 不存在的用户
	if _, err := db.GetUserByUsername(ctx, "bob"); err != ErrNotFound {
		t.Errorf("GetUserByUsername(bob) = %v, want ErrNotFound", err)
	}
}

// TestCreateUser_DuplicateUsername 验证 username 唯一约束。
func TestCreateUser_DuplicateUsername(t *testing.T) {
	dir := newTempDataDir(t)
	ctx := context.Background()
	db, err := Open(ctx, dir)
	if err != nil {
		t.Fatalf("Open() error: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	if _, err := db.CreateUser(ctx, "dup", "x", false); err != nil {
		t.Fatalf("first CreateUser: %v", err)
	}
	if _, err := db.CreateUser(ctx, "dup", "y", false); err == nil {
		t.Errorf("second CreateUser with dup username: nil err, want error")
	}
}

// TestCountUsers 验证空 / 非空统计。
func TestCountUsers(t *testing.T) {
	dir := newTempDataDir(t)
	ctx := context.Background()
	db, err := Open(ctx, dir)
	if err != nil {
		t.Fatalf("Open() error: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	n, err := db.CountUsers(ctx)
	if err != nil {
		t.Fatalf("CountUsers: %v", err)
	}
	if n != 0 {
		t.Errorf("empty CountUsers = %d, want 0", n)
	}

	if _, err := db.CreateUser(ctx, "u1", "p", false); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	n, _ = db.CountUsers(ctx)
	if n != 1 {
		t.Errorf("CountUsers = %d, want 1", n)
	}
}

// TestUpdateUserPassword 验证改密。
func TestUpdateUserPassword(t *testing.T) {
	dir := newTempDataDir(t)
	ctx := context.Background()
	db, err := Open(ctx, dir)
	if err != nil {
		t.Fatalf("Open() error: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	u, _ := db.CreateUser(ctx, "bob", "old", false)
	if err := db.UpdateUserPassword(ctx, u.ID, "new"); err != nil {
		t.Fatalf("UpdateUserPassword: %v", err)
	}
	if !VerifyPassword(u.PasswordHash, "new") {
		// 旧 hash 在内存里不会变，需要重新读
	}
	got, _ := db.GetUserByID(ctx, u.ID)
	if !VerifyPassword(got.PasswordHash, "new") {
		t.Errorf("VerifyPassword(new) = false, want true")
	}
	if VerifyPassword(got.PasswordHash, "old") {
		t.Errorf("VerifyPassword(old) = true, want false")
	}
	if got.MustChangePassword {
		t.Errorf("MustChangePassword = true, want false after update")
	}
}

// TestCreateSession_AndGet 验证 session 签发 + 查询 + 过期清理。
func TestCreateSession_AndGet(t *testing.T) {
	dir := newTempDataDir(t)
	ctx := context.Background()
	db, err := Open(ctx, dir)
	if err != nil {
		t.Fatalf("Open() error: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	u, _ := db.CreateUser(ctx, "carol", "p", false)
	s, err := db.CreateSession(ctx, u.ID, "127.0.0.1", "ua", 1*time.Hour)
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if len(s.Token) < 30 {
		t.Errorf("token too short: %d chars", len(s.Token))
	}
	if s.ExpiresAt <= s.CreatedAt {
		t.Errorf("ExpiresAt %d <= CreatedAt %d", s.ExpiresAt, s.CreatedAt)
	}

	got, err := db.GetSession(ctx, s.Token)
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	if got.UserID != u.ID {
		t.Errorf("UserID = %d, want %d", got.UserID, u.ID)
	}
}

// TestGetSession_Expired 验证过期 session 被清除并返回 ErrNotFound。
func TestGetSession_Expired(t *testing.T) {
	dir := newTempDataDir(t)
	ctx := context.Background()
	db, err := Open(ctx, dir)
	if err != nil {
		t.Fatalf("Open() error: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	u, _ := db.CreateUser(ctx, "dave", "p", false)
	// 1s TTL
	s, _ := db.CreateSession(ctx, u.ID, "127.0.0.1", "ua", 1*time.Second)
	// 模拟过期：把 expires_at 改到过去
	if _, err := db.SQLDB.ExecContext(ctx, `UPDATE sessions SET expires_at = ? WHERE token = ?`, time.Now().Unix()-1, s.Token); err != nil {
		t.Fatalf("update expires_at: %v", err)
	}
	if _, err := db.GetSession(ctx, s.Token); err != ErrNotFound {
		t.Errorf("GetSession expired: err = %v, want ErrNotFound", err)
	}
	// 过期行已被清除
	var n int
	_ = db.SQLDB.QueryRowContext(ctx, `SELECT COUNT(*) FROM sessions WHERE token = ?`, s.Token).Scan(&n)
	if n != 0 {
		t.Errorf("expired session not purged, n = %d", n)
	}
}

// TestDeleteSession + TestDeleteUserSessions 验证 logout 流程。
func TestDeleteSession_AndUser(t *testing.T) {
	dir := newTempDataDir(t)
	ctx := context.Background()
	db, err := Open(ctx, dir)
	if err != nil {
		t.Fatalf("Open() error: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	u, _ := db.CreateUser(ctx, "eve", "p", false)
	s1, _ := db.CreateSession(ctx, u.ID, "1.1.1.1", "ua1", time.Hour)
	s2, _ := db.CreateSession(ctx, u.ID, "2.2.2.2", "ua2", time.Hour)

	if err := db.DeleteSession(ctx, s1.Token); err != nil {
		t.Fatalf("DeleteSession: %v", err)
	}
	if _, err := db.GetSession(ctx, s1.Token); err != ErrNotFound {
		t.Errorf("s1 should be gone, got %v", err)
	}
	// s2 still valid
	if _, err := db.GetSession(ctx, s2.Token); err != nil {
		t.Errorf("s2 should still exist, got %v", err)
	}

	// 强制下线
	if err := db.DeleteUserSessions(ctx, u.ID); err != nil {
		t.Fatalf("DeleteUserSessions: %v", err)
	}
	if _, err := db.GetSession(ctx, s2.Token); err != ErrNotFound {
		t.Errorf("s2 should be gone after force logout, got %v", err)
	}
}

// TestPurgeExpiredSessions 验证批量清理。
func TestPurgeExpiredSessions(t *testing.T) {
	dir := newTempDataDir(t)
	ctx := context.Background()
	db, err := Open(ctx, dir)
	if err != nil {
		t.Fatalf("Open() error: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	u, _ := db.CreateUser(ctx, "frank", "p", false)
	// 2 个过期 + 1 个有效
	for i := 0; i < 2; i++ {
		s, _ := db.CreateSession(ctx, u.ID, "", "", time.Hour)
		_, _ = db.SQLDB.ExecContext(ctx, `UPDATE sessions SET expires_at = ? WHERE token = ?`, time.Now().Unix()-1, s.Token)
	}
	liveS, _ := db.CreateSession(ctx, u.ID, "", "", time.Hour)

	n, err := db.PurgeExpiredSessions(ctx)
	if err != nil {
		t.Fatalf("PurgeExpiredSessions: %v", err)
	}
	if n != 2 {
		t.Errorf("purged = %d, want 2", n)
	}
	if _, err := db.GetSession(ctx, liveS.Token); err != nil {
		t.Errorf("live session should remain, got %v", err)
	}
}

// TestWriteAudit + TestListAuditLogs 验证审计写入 + 查询。
func TestAuditLogs(t *testing.T) {
	dir := newTempDataDir(t)
	ctx := context.Background()
	db, err := Open(ctx, dir)
	if err != nil {
		t.Fatalf("Open() error: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	if err := db.WriteAudit(ctx, AuditLog{UserID: 0, Action: "init", Status: "ok", IP: "127.0.0.1"}); err != nil {
		t.Fatalf("WriteAudit 1: %v", err)
	}
	if err := db.WriteAudit(ctx, AuditLog{UserID: 1, Action: "login", Status: "ok", IP: "127.0.0.1", Details: "{}"}); err != nil {
		t.Fatalf("WriteAudit 2: %v", err)
	}
	if err := db.WriteAudit(ctx, AuditLog{UserID: 1, Action: "change_password", Status: "ok"}); err != nil {
		t.Fatalf("WriteAudit 3: %v", err)
	}

	logs, total, err := db.ListAuditLogs(ctx, 1, 10)
	if err != nil {
		t.Fatalf("ListAuditLogs: %v", err)
	}
	if total != 3 {
		t.Errorf("total = %d, want 3", total)
	}
	if len(logs) != 3 {
		t.Fatalf("len(logs) = %d, want 3", len(logs))
	}
	// DESC 排序：最新在 index 0
	if logs[0].Action != "change_password" {
		t.Errorf("logs[0].Action = %q, want change_password", logs[0].Action)
	}
}
