package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/cncachehub/server/internal/storage"
	"github.com/go-chi/chi/v5"
)

func buildPreheatRouter(opts Options) http.Handler {
	r := chi.NewRouter()
	r.Get("/api/preheat/tasks", preheatTaskListHandler(opts))
	r.Post("/api/preheat/tasks", preheatTaskCreateHandler(opts))
	r.Delete("/api/preheat/tasks/{id}", preheatTaskDeleteHandler(opts))
	r.Post("/api/preheat/tasks/{id}/run", preheatTaskRunHandler(opts))
	r.Post("/api/preheat/tasks/{id}/cancel", preheatTaskCancelHandler(opts))
	r.Get("/api/preheat/tasks/{id}/items", preheatTaskItemsHandler(opts))
	return r
}

func preheatOpts(db *storage.DB, run, cancel func(id int64) error) Options {
	return Options{
		ListPreheatTasks:  db.ListPreheatTasks,
		GetPreheatTask:    db.GetPreheatTask,
		CreatePreheatTask: db.CreatePreheatTask,
		DeletePreheatTask: db.DeletePreheatTask,
		ListPreheatItems:  db.ListPreheatItems,
		RunPreheatTask: func(ctx context.Context, id int64) error {
			if run != nil {
				return run(id)
			}
			return nil
		},
		CancelPreheatTask: func(id int64) bool {
			if cancel != nil {
				_ = cancel(id)
			}
			return true
		},
		SessionUserRole: func(ctx context.Context, r *http.Request) (string, int64, error) { return "admin", 1, nil },
	}
}

func TestPreheatTask_ListEmpty(t *testing.T) {
	dir := t.TempDir()
	db, _ := storage.Open(context.Background(), dir+"/test.db")
	t.Cleanup(func() { _ = db.Close() })
	r := buildPreheatRouter(preheatOpts(db, nil, nil))
	req := httptest.NewRequest("GET", "/api/preheat/tasks", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("code = %d, body=%s", rr.Code, rr.Body.String())
	}
	var resp PreheatTaskResponse
	_ = json.Unmarshal(rr.Body.Bytes(), &resp)
	if resp.Total != 0 {
		t.Errorf("Total = %d, want 0", resp.Total)
	}
}

func TestPreheatTask_Create_Validation(t *testing.T) {
	dir := t.TempDir()
	db, _ := storage.Open(context.Background(), dir+"/test.db")
	t.Cleanup(func() { _ = db.Close() })

	cases := []struct {
		name string
		body string
		code int
	}{
		{"missing_name", `{"kind":"docker","targets":["a"]}`, http.StatusBadRequest},
		{"bad_kind", `{"name":"t","kind":"hack","targets":["a"]}`, http.StatusBadRequest},
		{"empty_targets", `{"name":"t","kind":"docker","targets":[]}`, http.StatusBadRequest},
		{"dup_targets", `{"name":"t","kind":"docker","targets":["a"," a ","a"]}`, http.StatusCreated}, // 去重后 1 条
		{"ok", `{"name":"t","kind":"docker","targets":["nginx:alpine","redis:7"]}`, http.StatusCreated},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := buildPreheatRouter(preheatOpts(db, nil, nil))
			req := httptest.NewRequest("POST", "/api/preheat/tasks", bytes.NewBufferString(c.body))
			rr := httptest.NewRecorder()
			r.ServeHTTP(rr, req)
			if rr.Code != c.code {
				t.Errorf("code = %d, want %d, body=%s", rr.Code, c.code, rr.Body.String())
			}
		})
	}
}

func TestPreheatTask_CreateDedupe(t *testing.T) {
	dir := t.TempDir()
	db, _ := storage.Open(context.Background(), dir+"/test.db")
	t.Cleanup(func() { _ = db.Close() })
	r := buildPreheatRouter(preheatOpts(db, nil, nil))
	// "a" 和 " a " 视为重复（trim 后相等）；"a" 和 "b" 是不同。
	body := `{"name":"dedup","kind":"docker","targets":["a"," a ","a","b"]}`
	req := httptest.NewRequest("POST", "/api/preheat/tasks", bytes.NewBufferString(body))
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusCreated {
		t.Fatalf("code = %d, body=%s", rr.Code, rr.Body.String())
	}
	var t0 storage.PreheatTask
	_ = json.Unmarshal(rr.Body.Bytes(), &t0)
	if t0.ProgressTotal != 2 {
		t.Errorf("ProgressTotal = %d, want 2 (a/b after trim+dedup)", t0.ProgressTotal)
	}
}

func TestPreheatTask_Delete(t *testing.T) {
	dir := t.TempDir()
	db, _ := storage.Open(context.Background(), dir+"/test.db")
	t.Cleanup(func() { _ = db.Close() })
	task, _ := db.CreatePreheatTask(context.Background(), storage.PreheatTask{
		Name: "d", Kind: storage.PreheatKindDocker, Targets: []string{"a"},
	})
	r := buildPreheatRouter(preheatOpts(db, nil, nil))
	req := httptest.NewRequest("DELETE", "/api/preheat/tasks/"+strconv.FormatInt(task.ID, 10), nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusNoContent {
		t.Errorf("code = %d, want 204", rr.Code)
	}
	if _, err := db.GetPreheatTask(context.Background(), task.ID); err == nil {
		t.Error("Get after delete should error")
	}
}

func TestPreheatTask_Run_AdminOnly(t *testing.T) {
	dir := t.TempDir()
	db, _ := storage.Open(context.Background(), dir+"/test.db")
	t.Cleanup(func() { _ = db.Close() })
	task, _ := db.CreatePreheatTask(context.Background(), storage.PreheatTask{
		Name: "r", Kind: storage.PreheatKindDocker, Targets: []string{"a"},
	})

	// 非 admin → 403
	opts := preheatOpts(db, nil, nil)
	opts.SessionUserRole = func(ctx context.Context, r *http.Request) (string, int64, error) { return "user", 2, nil }
	r := buildPreheatRouter(opts)
	req := httptest.NewRequest("POST", "/api/preheat/tasks/"+strconv.FormatInt(task.ID, 10)+"/run", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Errorf("code = %d, want 403", rr.Code)
	}
}

func TestPreheatTask_Items(t *testing.T) {
	dir := t.TempDir()
	db, _ := storage.Open(context.Background(), dir+"/test.db")
	t.Cleanup(func() { _ = db.Close() })
	task, _ := db.CreatePreheatTask(context.Background(), storage.PreheatTask{
		Name: "i", Kind: storage.PreheatKindSteam, Targets: []string{"730", "2394010"},
	})
	r := buildPreheatRouter(preheatOpts(db, nil, nil))
	req := httptest.NewRequest("GET", "/api/preheat/tasks/"+strconv.FormatInt(task.ID, 10)+"/items", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("code = %d, body=%s", rr.Code, rr.Body.String())
	}
	var resp struct {
		Items []storage.PreheatItem `json:"items"`
		Total int                   `json:"total"`
	}
	_ = json.Unmarshal(rr.Body.Bytes(), &resp)
	if resp.Total != 2 {
		t.Errorf("Total = %d, want 2", resp.Total)
	}
}
