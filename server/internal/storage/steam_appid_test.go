package storage_test

import (
	"context"
	"testing"

	"github.com/cncachehub/server/internal/storage"
)

func TestSteamAppIDs_DefaultSeeded(t *testing.T) {
	db := openTestDB(t)
	items, err := db.ListSteamAppIDs(context.Background())
	if err != nil {
		t.Fatalf("ListSteamAppIDs: %v", err)
	}
	if len(items) < 5 {
		t.Errorf("seeded items = %d, want >= 5", len(items))
	}
	// 必须包含 CS2 (730)
	found := false
	for _, a := range items {
		if a.AppID == 730 && a.Name == "CS2 Dedicated Server" {
			found = true
		}
	}
	if !found {
		t.Error("CS2 (730) not in default seed")
	}
	// 默认 login_type=anonymous
	for _, a := range items {
		if a.LoginType != "anonymous" {
			t.Errorf("appid %d LoginType = %q, want anonymous", a.AppID, a.LoginType)
		}
	}
}

func TestSteamAppIDs_CRUD(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	// Create
	created, err := db.CreateSteamAppID(ctx, storage.SteamAppID{
		AppID: 12345, Name: "Test App", LoginType: "account", InstallDir: "/data/test", Enabled: true,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if created.ID == 0 {
		t.Error("created.ID = 0")
	}
	if created.CreatedAt == 0 || created.UpdatedAt == 0 {
		t.Error("timestamps not set")
	}

	// Get
	got, err := db.GetSteamAppID(ctx, created.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Name != "Test App" {
		t.Errorf("Name = %q", got.Name)
	}

	// Update
	enabled := false
	cache := int64(1234567)
	updated, err := db.UpdateSteamAppID(ctx, created.ID, storage.SteamAppIDPatch{
		Enabled:            &enabled,
		CacheBytesEstimate: &cache,
	})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if updated.Enabled {
		t.Error("Enabled not updated")
	}
	if updated.CacheBytesEstimate != 1234567 {
		t.Errorf("CacheBytesEstimate = %d, want 1234567", updated.CacheBytesEstimate)
	}

	// Delete
	if err := db.DeleteSteamAppID(ctx, created.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := db.GetSteamAppID(ctx, created.ID); err == nil {
		t.Error("Get after delete should error")
	}
}

func TestSteamAppIDs_DuplicateAppID(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	_, err := db.CreateSteamAppID(ctx, storage.SteamAppID{AppID: 99999, Name: "first"})
	if err != nil {
		t.Fatalf("first create: %v", err)
	}
	_, err = db.CreateSteamAppID(ctx, storage.SteamAppID{AppID: 99999, Name: "second"})
	if err == nil {
		t.Error("duplicate app_id should error")
	}
}

func TestSteamAppIDs_RecordPreheat(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	a, err := db.CreateSteamAppID(ctx, storage.SteamAppID{AppID: 88888, Name: "PreheatTest"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := db.RecordPreheatResult(ctx, a.ID, "ok", "preheat completed", 12345); err != nil {
		t.Fatalf("RecordPreheatResult: %v", err)
	}
	got, _ := db.GetSteamAppID(ctx, a.ID)
	if got.LastPreheatStatus != "ok" {
		t.Errorf("Status = %q, want ok", got.LastPreheatStatus)
	}
	if got.LastPreheatDurationMs != 12345 {
		t.Errorf("DurationMs = %d, want 12345", got.LastPreheatDurationMs)
	}
	if got.LastPreheatAt == 0 {
		t.Error("LastPreheatAt not set")
	}
	if got.LastPreheatMessage != "preheat completed" {
		t.Errorf("Message = %q", got.LastPreheatMessage)
	}
}
