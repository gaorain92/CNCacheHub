package storage

import (
	"context"
	"testing"
)

// TestUpstreamCredentials_SetGetClear 验证 SetUpstreamCredentials 的写入 + 读出 has* 标志 + 清空。
func TestUpstreamCredentials_SetGetClear(t *testing.T) {
	dir := newTempDataDir(t)
	ctx := context.Background()
	db, err := Open(ctx, dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	// 拿 dockerhub（migration seed）
	r, err := db.GetUpstreamByName(ctx, "dockerhub")
	if err != nil {
		t.Fatalf("GetUpstreamByName: %v", err)
	}
	if r.HasPassword || r.HasToken || r.Username != "" {
		t.Fatalf("expected fresh seed has no creds, got %+v", r)
	}

	// 1. 写 username + password
	un := "alice"
	pw := []byte("encrypted-password-bytes")
	err = db.SetUpstreamCredentials(ctx, "dockerhub", RegistryCredentialsPatch{
		Username: &un,
		Password: &pw,
	})
	if err != nil {
		t.Fatalf("SetUpstreamCredentials: %v", err)
	}

	r2, _ := db.GetUpstreamByName(ctx, "dockerhub")
	if r2.Username != "alice" {
		t.Errorf("username = %q", r2.Username)
	}
	if !r2.HasPassword {
		t.Error("hasPassword should be true")
	}
	if r2.HasToken {
		t.Error("hasToken should still be false")
	}
	if string(r2.PasswordEnc) != "encrypted-password-bytes" {
		t.Errorf("PasswordEnc = %q", r2.PasswordEnc)
	}

	// 2. 加 token
	tk := []byte("encrypted-token-bytes")
	err = db.SetUpstreamCredentials(ctx, "dockerhub", RegistryCredentialsPatch{
		Token: &tk,
	})
	if err != nil {
		t.Fatal(err)
	}
	r3, _ := db.GetUpstreamByName(ctx, "dockerhub")
	if !r3.HasToken {
		t.Error("hasToken should be true after setting token")
	}

	// 3. 清空 password
	err = db.SetUpstreamCredentials(ctx, "dockerhub", RegistryCredentialsPatch{
		ClearPassword: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	r4, _ := db.GetUpstreamByName(ctx, "dockerhub")
	if r4.HasPassword {
		t.Error("hasPassword should be false after clear")
	}
	if r4.PasswordEnc != nil {
		t.Errorf("PasswordEnc should be nil, got %d bytes", len(r4.PasswordEnc))
	}
	// token 还在
	if !r4.HasToken {
		t.Error("hasToken should still be true (not cleared)")
	}

	// 4. 清空 token
	err = db.SetUpstreamCredentials(ctx, "dockerhub", RegistryCredentialsPatch{
		ClearToken: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	r5, _ := db.GetUpstreamByName(ctx, "dockerhub")
	if r5.HasToken {
		t.Error("hasToken should be false after clear")
	}

	// 5. no-op patch（所有字段 nil + 不 clear）应该不报错也不变
	before := r5.Username
	err = db.SetUpstreamCredentials(ctx, "dockerhub", RegistryCredentialsPatch{})
	if err != nil {
		t.Fatal(err)
	}
	r6, _ := db.GetUpstreamByName(ctx, "dockerhub")
	if r6.Username != before {
		t.Errorf("no-op patch changed username: %q -> %q", before, r6.Username)
	}
}

// TestUpstreamCredentials_NotFound 验证对不存在的 name 报 ErrNotFound。
func TestUpstreamCredentials_NotFound(t *testing.T) {
	dir := newTempDataDir(t)
	ctx := context.Background()
	db, err := Open(ctx, dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	un := "x"
	err = db.SetUpstreamCredentials(ctx, "nonexistent-registry", RegistryCredentialsPatch{
		Username: &un,
	})
	if err == nil {
		t.Error("expected error for nonexistent registry")
	}
}
