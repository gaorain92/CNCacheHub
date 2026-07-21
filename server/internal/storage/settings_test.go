package storage

import (
	"context"
	"testing"
)

// TestOpen_SystemSettingsSeed 验证 0007 跑完有 seed 行。
func TestOpen_SystemSettingsSeed(t *testing.T) {
	dir := newTempDataDir(t)
	ctx := context.Background()
	db, err := Open(ctx, dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	settings, err := db.ListSettings(ctx)
	if err != nil {
		t.Fatalf("ListSettings: %v", err)
	}
	if len(settings) == 0 {
		t.Fatal("expected seeded settings, got none")
	}
	// 应该有 small_vps_opt / reserve_space_gb / max_object_size_mb / cache_total_gb
	keys := make(map[string]string)
	for _, s := range settings {
		keys[s.Key] = s.Value
	}
	for _, k := range []string{
		SettingSmallVPSOpt,
		SettingReserveSpaceGB,
		SettingMaxObjectSizeMB,
		SettingCacheTotalGB,
	} {
		if _, ok := keys[k]; !ok {
			t.Errorf("missing seed key %q", k)
		}
	}
}

// TestSetSetting_AndGet 验证 upsert。
func TestSetSetting_AndGet(t *testing.T) {
	dir := newTempDataDir(t)
	ctx := context.Background()
	db, err := Open(ctx, dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	if err := db.SetSetting(ctx, SettingSmallVPSOpt, "true", 1); err != nil {
		t.Fatalf("SetSetting: %v", err)
	}
	got, err := db.GetSetting(ctx, SettingSmallVPSOpt)
	if err != nil {
		t.Fatalf("GetSetting: %v", err)
	}
	if got.Value != "true" {
		t.Errorf("Value = %q, want true", got.Value)
	}
	if got.UpdatedBy != 1 {
		t.Errorf("UpdatedBy = %d, want 1", got.UpdatedBy)
	}

	// 覆写
	if err := db.SetSetting(ctx, SettingSmallVPSOpt, "false", 2); err != nil {
		t.Fatalf("SetSetting overwrite: %v", err)
	}
	got, _ = db.GetSetting(ctx, SettingSmallVPSOpt)
	if got.Value != "false" {
		t.Errorf("Value = %q, want false", got.Value)
	}
	if got.UpdatedBy != 2 {
		t.Errorf("UpdatedBy = %d, want 2", got.UpdatedBy)
	}
}

// TestGetString_Fallback 验证缺字段走 fallback。
func TestGetString_Fallback(t *testing.T) {
	dir := newTempDataDir(t)
	ctx := context.Background()
	db, err := Open(ctx, dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	v := db.GetString(ctx, "non_existent_key", "fallback")
	if v != "fallback" {
		t.Errorf("GetString = %q, want fallback", v)
	}
}

// TestSetMany_All 批量 upsert。
func TestSetMany_All(t *testing.T) {
	dir := newTempDataDir(t)
	ctx := context.Background()
	db, err := Open(ctx, dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	err = db.SetMany(ctx, map[string]string{
		SettingSmallVPSOpt:     "true",
		SettingReserveSpaceGB:  "10",
		SettingCacheTotalGB:    "50",
	}, 99)
	if err != nil {
		t.Fatalf("SetMany: %v", err)
	}
	if v := db.GetString(ctx, SettingSmallVPSOpt, ""); v != "true" {
		t.Errorf("small_vps_opt = %q", v)
	}
	if v := db.GetString(ctx, SettingReserveSpaceGB, ""); v != "10" {
		t.Errorf("reserve_space_gb = %q", v)
	}
}
