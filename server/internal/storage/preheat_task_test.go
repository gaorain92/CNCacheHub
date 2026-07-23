package storage_test

import (
	"context"
	"testing"

	"github.com/cncachehub/server/internal/storage"
)

func TestPreheatTask_CreateList(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	task, err := db.CreatePreheatTask(ctx, storage.PreheatTask{
		Name:    "common-images",
		Kind:    storage.PreheatKindDocker,
		Targets: []string{"nginx:alpine", "redis:7", "alpine:3.19"},
		Enabled: true,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if task.ID == 0 {
		t.Error("task.ID = 0")
	}
	if task.ProgressTotal != 3 {
		t.Errorf("ProgressTotal = %d, want 3", task.ProgressTotal)
	}
	if task.Status != storage.PreheatStatusPending {
		t.Errorf("Status = %q, want pending", task.Status)
	}

	// List
	list, err := db.ListPreheatTasks(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("List len = %d, want 1", len(list))
	}
	if list[0].Name != "common-images" {
		t.Errorf("Name = %q", list[0].Name)
	}
	if len(list[0].Targets) != 3 {
		t.Errorf("Targets len = %d, want 3", len(list[0].Targets))
	}
}

func TestPreheatTask_CreateRequiresTargets(t *testing.T) {
	db := openTestDB(t)
	_, err := db.CreatePreheatTask(context.Background(), storage.PreheatTask{
		Name: "empty", Kind: storage.PreheatKindDocker, Targets: nil,
	})
	if err == nil {
		t.Error("empty targets should error")
	}
}

func TestPreheatTask_Items(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	task, _ := db.CreatePreheatTask(ctx, storage.PreheatTask{
		Name: "t", Kind: storage.PreheatKindSteam,
		Targets: []string{"730", "2394010"},
	})
	items, err := db.ListPreheatItems(ctx, task.ID)
	if err != nil {
		t.Fatalf("ListItems: %v", err)
	}
	if len(items) != 2 {
		t.Errorf("items len = %d, want 2", len(items))
	}
	for i, it := range items {
		if it.Status != storage.PreheatItemPending {
			t.Errorf("items[%d].Status = %q", i, it.Status)
		}
	}
	// Update first item → running → done
	if err := db.UpdatePreheatItem(ctx, items[0].ID, storage.PreheatItemRunning, "", 0, 1700000000, 0); err != nil {
		t.Fatalf("UpdateItem: %v", err)
	}
	if err := db.UpdatePreheatItem(ctx, items[0].ID, storage.PreheatItemDone, "", 1024, 1700000000, 1700000001); err != nil {
		t.Fatalf("UpdateItem done: %v", err)
	}
	items2, _ := db.ListPreheatItems(ctx, task.ID)
	if items2[0].Status != storage.PreheatItemDone {
		t.Errorf("items[0].Status = %q, want done", items2[0].Status)
	}
	if items2[0].BytesAdded != 1024 {
		t.Errorf("items[0].BytesAdded = %d, want 1024", items2[0].BytesAdded)
	}
}

func TestPreheatTask_Progress(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	task, _ := db.CreatePreheatTask(ctx, storage.PreheatTask{
		Name: "p", Kind: storage.PreheatKindDocker, Targets: []string{"a", "b", "c"},
	})
	if err := db.UpdatePreheatTaskProgress(ctx, task.ID, 1, 100); err != nil {
		t.Fatalf("progress: %v", err)
	}
	if err := db.UpdatePreheatTaskProgress(ctx, task.ID, 2, 250); err != nil {
		t.Fatalf("progress 2: %v", err)
	}
	got, _ := db.GetPreheatTask(ctx, task.ID)
	if got.ProgressDone != 3 {
		t.Errorf("ProgressDone = %d, want 3", got.ProgressDone)
	}
	if got.ProgressBytes != 350 {
		t.Errorf("ProgressBytes = %d, want 350", got.ProgressBytes)
	}
}

func TestPreheatTask_DeleteCascades(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	task, _ := db.CreatePreheatTask(ctx, storage.PreheatTask{
		Name: "d", Kind: storage.PreheatKindDocker, Targets: []string{"a"},
	})
	if err := db.DeletePreheatTask(ctx, task.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := db.GetPreheatTask(ctx, task.ID); err == nil {
		t.Error("Get after delete should error")
	}
	items, _ := db.ListPreheatItems(ctx, task.ID)
	if len(items) != 0 {
		t.Errorf("items after delete = %d, want 0", len(items))
	}
}

func TestPreheatTask_UpdateStatus(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	task, _ := db.CreatePreheatTask(ctx, storage.PreheatTask{
		Name: "u", Kind: storage.PreheatKindDocker, Targets: []string{"a"},
	})
	if err := db.UpdatePreheatTaskStatus(ctx, task.ID, storage.PreheatStatusRunning, "", 0); err != nil {
		t.Fatalf("UpdateStatus: %v", err)
	}
	got, _ := db.GetPreheatTask(ctx, task.ID)
	if got.Status != storage.PreheatStatusRunning {
		t.Errorf("Status = %q", got.Status)
	}
	if err := db.UpdatePreheatTaskStatus(ctx, task.ID, storage.PreheatStatusDone, "", 12345); err != nil {
		t.Fatalf("UpdateStatus done: %v", err)
	}
	got, _ = db.GetPreheatTask(ctx, task.ID)
	if got.LastDurationMs != 12345 {
		t.Errorf("LastDurationMs = %d", got.LastDurationMs)
	}
}
