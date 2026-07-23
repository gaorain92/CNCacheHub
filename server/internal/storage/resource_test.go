package storage_test

import (
	"context"
	"testing"

	"github.com/cncachehub/server/internal/storage"
)

func TestResourceRule_DefaultSeeded(t *testing.T) {
	db := openTestDB(t)
	rules, err := db.ListResourceRules(context.Background())
	if err != nil {
		t.Fatalf("ListResourceRules: %v", err)
	}
	if len(rules) < 4 {
		t.Errorf("seeded = %d, want >= 4", len(rules))
	}
	// 必须包含 github-release / huggingface / playwright / terraform
	want := map[string]bool{"github-release": false, "huggingface": false, "playwright": false, "terraform": false}
	for _, r := range rules {
		if _, ok := want[r.Name]; ok {
			want[r.Name] = true
		}
	}
	for k, v := range want {
		if !v {
			t.Errorf("default rule %q not seeded", k)
		}
	}
}

func TestResourceRule_CRUD(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	// Create
	created, err := db.CreateResourceRule(ctx, storage.ResourceRule{
		Name: "test-rule", Kind: "custom", UpstreamURL: "https://example.com/",
		Description: "test", Enabled: true, DefaultTTLSeconds: 3600,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if created.ID == 0 {
		t.Error("ID = 0")
	}
	if created.UpstreamURL != "https://example.com" { // 末尾 / 应被 trim
		t.Errorf("UpstreamURL = %q, want trimmed", created.UpstreamURL)
	}

	// Get by id
	got, err := db.GetResourceRule(ctx, created.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Name != "test-rule" {
		t.Errorf("Name = %q", got.Name)
	}

	// Get by name
	byName, err := db.GetResourceRuleByName(ctx, "test-rule")
	if err != nil {
		t.Fatalf("GetByName: %v", err)
	}
	if byName.ID != created.ID {
		t.Errorf("ID = %d, want %d", byName.ID, created.ID)
	}

	// Update
	desc := "updated"
	enabled := false
	upd, err := db.UpdateResourceRule(ctx, created.ID, storage.ResourceRulePatch{
		Description: &desc, Enabled: &enabled,
	})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if upd.Description != "updated" || upd.Enabled {
		t.Error("Update not persisted")
	}

	// Delete
	if err := db.DeleteResourceRule(ctx, created.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := db.GetResourceRule(ctx, created.ID); err == nil {
		t.Error("Get after delete should error")
	}
}

func TestResourceCache_UpsertAndHit(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	// 补建 unique index（migration 之前可能没加；不影响生产 DB）
	if _, err := db.SQLDB.ExecContext(ctx, `CREATE UNIQUE INDEX IF NOT EXISTS uniq_resource_cache_rule_path ON resource_cache_entries(rule_id, path)`); err != nil {
		t.Fatalf("create unique index: %v", err)
	}

	rules, _ := db.ListResourceRules(ctx)
	if len(rules) == 0 {
		t.Fatal("no seeded rule")
	}
	ruleID := rules[0].ID

	// 1st upsert
	e1, err := db.UpsertResourceCacheEntry(ctx, storage.ResourceCacheEntry{
		RuleID: ruleID, Path: "foo/bar.tar.gz", SizeBytes: 1024, ExpiresAt: 0,
		ContentType: "application/gzip", StoragePath: "/var/cache/cncachehub/resource/foo",
	})
	if err != nil {
		t.Fatalf("Upsert 1: %v", err)
	}
	if e1.HitCount != 0 {
		t.Errorf("HitCount = %d, want 0", e1.HitCount)
	}

	// 2nd upsert (same path) → update size + reset hits? No, ON CONFLICT 不动 hit_count
	e2, err := db.UpsertResourceCacheEntry(ctx, storage.ResourceCacheEntry{
		RuleID: ruleID, Path: "foo/bar.tar.gz", SizeBytes: 2048, ExpiresAt: 0,
		ContentType: "application/gzip", StoragePath: "/var/cache/cncachehub/resource/foo",
	})
	if err != nil {
		t.Fatalf("Upsert 2: %v", err)
	}
	if e2.SizeBytes != 2048 {
		t.Errorf("SizeBytes = %d, want 2048 (update)", e2.SizeBytes)
	}
	if e2.HitCount != 0 {
		t.Errorf("HitCount = %d, want 0 (ON CONFLICT 不动 hit)", e2.HitCount)
	}

	// Bump hit
	if err := db.BumpResourceCacheHit(ctx, e2.ID); err != nil {
		t.Fatalf("BumpHit: %v", err)
	}
	if err := db.BumpResourceCacheHit(ctx, e2.ID); err != nil {
		t.Fatalf("BumpHit 2: %v", err)
	}
	e3, _ := db.GetResourceCacheEntry(ctx, ruleID, "foo/bar.tar.gz")
	if e3.HitCount != 2 {
		t.Errorf("HitCount = %d, want 2", e3.HitCount)
	}

	// List
	list, err := db.ListResourceCache(ctx, ruleID, 10)
	if err != nil {
		t.Fatalf("ListResourceCache: %v", err)
	}
	if len(list) != 1 {
		t.Errorf("list len = %d, want 1", len(list))
	}
}

func TestResourceCache_DeleteCascades(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	if _, err := db.SQLDB.ExecContext(ctx, `CREATE UNIQUE INDEX IF NOT EXISTS uniq_resource_cache_rule_path ON resource_cache_entries(rule_id, path)`); err != nil {
		t.Fatalf("create unique index: %v", err)
	}
	rules, _ := db.ListResourceRules(ctx)
	ruleID := rules[0].ID
	_, _ = db.UpsertResourceCacheEntry(ctx, storage.ResourceCacheEntry{
		RuleID: ruleID, Path: "x", SizeBytes: 1,
	})
	if err := db.DeleteResourceRule(ctx, ruleID); err != nil {
		t.Fatalf("DeleteRule: %v", err)
	}
	list, _ := db.ListResourceCache(ctx, ruleID, 10)
	if len(list) != 0 {
		t.Errorf("cache after rule delete = %d, want 0 (cascade)", len(list))
	}
}
