package storage_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/cncachehub/server/internal/storage"
)

func openTestDB(t *testing.T) *storage.DB {
	t.Helper()
	dir := t.TempDir()
	db, err := storage.Open(context.Background(), filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("storage.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func TestDNSConfig_DefaultSeeded(t *testing.T) {
	db := openTestDB(t)
	cfg, err := db.GetDNSConfig(context.Background())
	if err != nil {
		t.Fatalf("GetDNSConfig: %v", err)
	}
	if cfg.ID != 1 {
		t.Errorf("id = %d, want 1", cfg.ID)
	}
	if cfg.ListenAddr == "" {
		t.Error("ListenAddr empty")
	}
	if cfg.Upstream == "" {
		t.Error("Upstream empty")
	}
	if cfg.AnswerIP == "" {
		t.Error("AnswerIP empty")
	}
	if len(cfg.DomainRules) < 5 {
		t.Errorf("DomainRules = %d, want >= 5", len(cfg.DomainRules))
	}
	if cfg.UpdatedAt == 0 {
		t.Error("UpdatedAt not set")
	}
}

func TestDNSConfig_UpdatePartial(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	// 1) 启用 + 改 AnswerIP
	enabled := true
	newIP := "192.168.1.10"
	cfg1, err := db.UpdateDNSConfig(ctx, storage.DNSConfigPatch{
		Enabled:  &enabled,
		AnswerIP: &newIP,
	})
	if err != nil {
		t.Fatalf("UpdateDNSConfig(1): %v", err)
	}
	if !cfg1.Enabled {
		t.Error("Enabled not set")
	}
	if cfg1.AnswerIP != newIP {
		t.Errorf("AnswerIP = %q, want %q", cfg1.AnswerIP, newIP)
	}
	// ListenAddr 没改
	if cfg1.ListenAddr == "" {
		t.Error("ListenAddr lost")
	}

	// 2) 只改 domain_rules
	newRules := []string{"*.example.com", "foo.bar"}
	// Unix 秒级时间戳；为保证 UpdatedAt 严格前进，sleep 1.1s
	// （用单调时钟可避免，但 DB schema 用 Unix 秒，简单 sleep 更稳）
	time.Sleep(1100 * time.Millisecond)
	cfg2, err := db.UpdateDNSConfig(ctx, storage.DNSConfigPatch{
		DomainRules: &newRules,
	})
	if err != nil {
		t.Fatalf("UpdateDNSConfig(2): %v", err)
	}
	if len(cfg2.DomainRules) != 2 {
		t.Errorf("DomainRules len = %d, want 2", len(cfg2.DomainRules))
	}
	if cfg2.DomainRules[0] != "*.example.com" {
		t.Errorf("DomainRules[0] = %q", cfg2.DomainRules[0])
	}
	// Enabled/AnswerIP 保留
	if !cfg2.Enabled {
		t.Error("Enabled lost after rule update")
	}
	if cfg2.AnswerIP != newIP {
		t.Errorf("AnswerIP lost: %q", cfg2.AnswerIP)
	}
	if cfg2.UpdatedAt <= cfg1.UpdatedAt {
		t.Errorf("UpdatedAt not advanced: %d <= %d", cfg2.UpdatedAt, cfg1.UpdatedAt)
	}
}

func TestDNSConfig_DisableKeepsRules(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	disabled := false
	rules := []string{"*.keep.com"}
	cfg, err := db.UpdateDNSConfig(ctx, storage.DNSConfigPatch{
		Enabled:     &disabled,
		DomainRules: &rules,
	})
	if err != nil {
		t.Fatalf("UpdateDNSConfig: %v", err)
	}
	if cfg.Enabled {
		t.Error("Enabled should be false")
	}
	if len(cfg.DomainRules) != 1 || cfg.DomainRules[0] != "*.keep.com" {
		t.Errorf("DomainRules not persisted: %v", cfg.DomainRules)
	}
}
