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

func buildSteamAppIDRouter(opts Options) http.Handler {
	r := chi.NewRouter()
	r.Get("/api/steamcmd/appids", steamAppIDListHandler(opts))
	r.Post("/api/steamcmd/appids", steamAppIDCreateHandler(opts))
	r.Patch("/api/steamcmd/appids/{id}", steamAppIDPatchHandler(opts))
	r.Delete("/api/steamcmd/appids/{id}", steamAppIDDeleteHandler(opts))
	r.Post("/api/steamcmd/appids/{id}/preheat", steamAppIDPreheatHandler(opts))
	return r
}

func adminOpts(db *storage.DB) Options {
	return Options{
		ListSteamAppIDs:     db.ListSteamAppIDs,
		GetSteamAppID:       db.GetSteamAppID,
		CreateSteamAppID:    db.CreateSteamAppID,
		UpdateSteamAppID:    db.UpdateSteamAppID,
		DeleteSteamAppID:    db.DeleteSteamAppID,
		RecordPreheatResult: db.RecordPreheatResult,
		SessionUserRole:     func(ctx context.Context, r *http.Request) (string, int64, error) { return "admin", 1, nil },
	}
}

func TestSteamAppID_List_DefaultSeed(t *testing.T) {
	dir := t.TempDir()
	db, err := storage.Open(context.Background(), dir+"/test.db")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	r := buildSteamAppIDRouter(adminOpts(db))
	req := httptest.NewRequest("GET", "/api/steamcmd/appids", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("code = %d, body=%s", rr.Code, rr.Body.String())
	}
	var resp SteamAppIDResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("json: %v", err)
	}
	if resp.Total < 5 {
		t.Errorf("Total = %d, want >= 5", resp.Total)
	}
	// 必须含 CS2
	for _, a := range resp.Items {
		if a.AppID == 730 {
			return
		}
	}
	t.Error("CS2 (730) not in list")
}

func TestSteamAppID_Create_AdminOnly(t *testing.T) {
	dir := t.TempDir()
	db, _ := storage.Open(context.Background(), dir+"/test.db")
	t.Cleanup(func() { _ = db.Close() })

	// 非 admin
	opts := adminOpts(db)
	opts.SessionUserRole = func(ctx context.Context, r *http.Request) (string, int64, error) { return "user", 2, nil }
	r := buildSteamAppIDRouter(opts)
	req := httptest.NewRequest("POST", "/api/steamcmd/appids", bytes.NewBufferString(`{"appId":12345,"name":"X"}`))
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Errorf("code = %d, want 403", rr.Code)
	}
}

func TestSteamAppID_Create_Validation(t *testing.T) {
	dir := t.TempDir()
	db, _ := storage.Open(context.Background(), dir+"/test.db")
	t.Cleanup(func() { _ = db.Close() })

	cases := []struct {
		name string
		body string
		code int
	}{
		{"missing_name", `{"appId":12345}`, http.StatusBadRequest},
		{"invalid_app_id", `{"appId":-1,"name":"X"}`, http.StatusBadRequest},
		{"bad_login_type", `{"appId":12345,"name":"X","loginType":"hacker"}`, http.StatusBadRequest},
		{"ok", `{"appId":12345,"name":"Test App","loginType":"anonymous"}`, http.StatusCreated},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := buildSteamAppIDRouter(adminOpts(db))
			req := httptest.NewRequest("POST", "/api/steamcmd/appids", bytes.NewBufferString(c.body))
			rr := httptest.NewRecorder()
			r.ServeHTTP(rr, req)
			if rr.Code != c.code {
				t.Errorf("code = %d, want %d, body=%s", rr.Code, c.code, rr.Body.String())
			}
		})
	}
}

func TestSteamAppID_Patch_And_Delete(t *testing.T) {
	dir := t.TempDir()
	db, _ := storage.Open(context.Background(), dir+"/test.db")
	t.Cleanup(func() { _ = db.Close() })

	// 先建
	a, err := db.CreateSteamAppID(context.Background(), storage.SteamAppID{AppID: 55555, Name: "PatchTest"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	r := buildSteamAppIDRouter(adminOpts(db))
	// PATCH
	body := `{"name":"renamed","installDir":"/x/y","enabled":false}`
	req := httptest.NewRequest("PATCH", "/api/steamcmd/appids/"+itoa(a.ID), bytes.NewBufferString(body))
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("PATCH code = %d, body=%s", rr.Code, rr.Body.String())
	}
	var got storage.SteamAppID
	_ = json.Unmarshal(rr.Body.Bytes(), &got)
	if got.Name != "renamed" {
		t.Errorf("Name = %q, want renamed", got.Name)
	}
	if got.Enabled {
		t.Error("Enabled should be false")
	}

	// DELETE
	req = httptest.NewRequest("DELETE", "/api/steamcmd/appids/"+itoa(a.ID), nil)
	rr = httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusNoContent {
		t.Errorf("DELETE code = %d, want 204", rr.Code)
	}
	if _, err := db.GetSteamAppID(context.Background(), a.ID); err == nil {
		t.Error("Get after delete should error")
	}
}

func TestSteamAppID_Preheat_Anonymous(t *testing.T) {
	dir := t.TempDir()
	db, _ := storage.Open(context.Background(), dir+"/test.db")
	t.Cleanup(func() { _ = db.Close() })

	// 用一个和 seed 不冲突的 AppID（seed 包含 730/2394010 等）
	a, err := db.CreateSteamAppID(context.Background(), storage.SteamAppID{AppID: 99900001, Name: "PreheatAnony", LoginType: "anonymous"})
	if err != nil {
		t.Fatalf("CreateSteamAppID: %v", err)
	}
	t.Logf("a.ID = %d (type %T), itoa = %q, err=%v", a.ID, a.ID, itoa(a.ID), err)

	r := buildSteamAppIDRouter(adminOpts(db))
	req := httptest.NewRequest("POST", "/api/steamcmd/appids/"+itoa(a.ID)+"/preheat", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	// 行为取决于测试机有没有 docker：
	//   - 有 docker：202 Accepted + status=running（fire-and-forget 起后台 docker run）
	//   - 无 docker：200 OK + status=skipped（提示安装 docker 或用 --dns 客户端）
	// 都算合法
	if rr.Code != http.StatusOK && rr.Code != http.StatusAccepted {
		t.Fatalf("code = %d, body=%s", rr.Code, rr.Body.String())
	}
	var resp preheatResponse
	_ = json.Unmarshal(rr.Body.Bytes(), &resp)
	if resp.Status != "skipped" && resp.Status != "running" {
		t.Errorf("Status = %q, want skipped or running", resp.Status)
	}
	if resp.Message == "" {
		t.Error("Message empty")
	}
}

func TestSteamAppID_Preheat_AccountSkipped(t *testing.T) {
	dir := t.TempDir()
	db, _ := storage.Open(context.Background(), dir+"/test.db")
	t.Cleanup(func() { _ = db.Close() })

	a, _ := db.CreateSteamAppID(context.Background(), storage.SteamAppID{AppID: 99900002, Name: "AccApp", LoginType: "account"})

	r := buildSteamAppIDRouter(adminOpts(db))
	req := httptest.NewRequest("POST", "/api/steamcmd/appids/"+itoa(a.ID)+"/preheat", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("code = %d", rr.Code)
	}
	var resp preheatResponse
	_ = json.Unmarshal(rr.Body.Bytes(), &resp)
	if resp.Status != "skipped" {
		t.Errorf("Status = %q, want skipped (account requires interactive login)", resp.Status)
	}
	if !contains(resp.Message, "账号") {
		t.Errorf("Message should mention 账号, got: %s", resp.Message)
	}
}

// helpers
func itoa(n int64) string {
	return strconv.FormatInt(n, 10)
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
