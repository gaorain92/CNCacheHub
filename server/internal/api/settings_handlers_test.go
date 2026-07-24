package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/cncachehub/server/internal/storage"
)

func settingsTestHandler(t *testing.T) (http.Handler, *storage.DB) {
	t.Helper()
	fdb := newFakeAuthDB(t)
	_, _ = fdb.CreateUser(context.Background(), "admin", "admin1234", true)
	return newSettingsHandler(fdb.DB), fdb.DB
}

func newSettingsHandler(db *storage.DB) http.Handler {
	return NewRouter(Options{
		AuthDB: db,
		SessionUserRole: func(ctx context.Context, r *http.Request) (string, int64, error) {
			return "admin", 1, nil
		},
		GetSettings: func(ctx context.Context) (SystemSettings, error) {
			s, err := db.ListSettings(ctx)
			if err != nil {
				return SystemSettings{}, err
			}
			out := SystemSettings{}
			for _, x := range s {
				switch x.Key {
				case storage.SettingSmallVPSOpt:
					out.SmallVPSOpt = x.Value == "true"
				case storage.SettingReserveSpaceGB:
					out.ReserveSpaceGB = parseIntOr(x.Value, 5)
				case storage.SettingMaxObjectSizeMB:
					out.MaxObjectSizeMB = parseIntOr(x.Value, 1024)
				case storage.SettingCacheTotalGB:
					out.CacheTotalGB = parseIntOr(x.Value, 20)
				case storage.SettingCleanupTriggerPct:
					out.CleanupTriggerPct = parseIntOr(x.Value, 80)
				case storage.SettingCleanupTargetPct:
					out.CleanupTargetPct = parseIntOr(x.Value, 60)
				}
			}
			return out, nil
		},
		UpdateSettings: func(ctx context.Context, patch SettingsPatch, userID int64) (SystemSettings, error) {
			kvs := map[string]string{}
			if patch.SmallVPSOpt != nil {
				if *patch.SmallVPSOpt {
					kvs[storage.SettingSmallVPSOpt] = "true"
				} else {
					kvs[storage.SettingSmallVPSOpt] = "false"
				}
			}
			if patch.ReserveSpaceGB != nil {
				kvs[storage.SettingReserveSpaceGB] = strconv.Itoa(*patch.ReserveSpaceGB)
			}
			if patch.MaxObjectSizeMB != nil {
				kvs[storage.SettingMaxObjectSizeMB] = strconv.Itoa(*patch.MaxObjectSizeMB)
			}
			if patch.CacheTotalGB != nil {
				kvs[storage.SettingCacheTotalGB] = strconv.Itoa(*patch.CacheTotalGB)
			}
			if patch.CleanupTriggerPct != nil {
				kvs[storage.SettingCleanupTriggerPct] = strconv.Itoa(*patch.CleanupTriggerPct)
			}
			if patch.CleanupTargetPct != nil {
				kvs[storage.SettingCleanupTargetPct] = strconv.Itoa(*patch.CleanupTargetPct)
			}
			if err := db.SetMany(ctx, kvs, userID); err != nil {
				return SystemSettings{}, err
			}
			s, _ := db.ListSettings(ctx)
			out := SystemSettings{}
			for _, x := range s {
				if x.Key == storage.SettingSmallVPSOpt {
					out.SmallVPSOpt = x.Value == "true"
				}
			}
			return out, nil
		},
		DryRunCleanup: func(ctx context.Context, id int64) (CleanupReport, error) {
			t, err := db.GetCleanupTaskByID(ctx, id)
			if err != nil {
				return CleanupReport{}, err
			}
			switch t.Strategy {
			case "lru":
				rep, _ := db.RunLRU(ctx, id, t.ThresholdSeconds, 200, true)
				return cleanupToAPI(rep), nil
			case "capacity":
				rep, _ := db.RunCapacity(ctx, id, t.ThresholdBytes, 200, true)
				return cleanupToAPI(rep), nil
			}
			return CleanupReport{}, nil
		},
	})
}

func cleanupToAPI(r storage.CleanupReport) CleanupReport {
	return CleanupReport{
		TaskID: r.TaskID, Strategy: r.Strategy,
		FreedCount: r.FreedCount, FreedBytes: r.FreedBytes,
		BeforeCount: r.BeforeCount, BeforeBytes: r.BeforeBytes,
		AfterCount: r.AfterCount, AfterBytes: r.AfterBytes,
		DurationMs: r.DurationMs,
	}
}

func parseIntOr(s string, fallback int) int {
	if s == "" {
		return fallback
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return fallback
	}
	return n
}

func patchJSON(t *testing.T, h http.Handler, path string, body any, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		_ = json.NewEncoder(&buf).Encode(body)
	}
	req := httptest.NewRequest(http.MethodPatch, path, &buf)
	req.Header.Set("Content-Type", "application/json")
	for _, c := range cookies {
		req.AddCookie(c)
	}
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

// TestSettings_AuthRequired 验证未登录 401。
func TestSettings_AuthRequired(t *testing.T) {
	h, _ := settingsTestHandler(t)
	rr := getJSON(t, h, "/api/settings")
	if rr.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", rr.Code)
	}
}

// TestSettings_GetAfterLogin 登录后能读默认。
func TestSettings_GetAfterLogin(t *testing.T) {
	h, _ := settingsTestHandler(t)
	rr := postJSON(t, h, "/api/auth/login", LoginRequest{Username: "admin", Password: "admin1234"})
	var cookie *http.Cookie
	for _, ck := range rr.Result().Cookies() {
		if ck.Name == SessionCookieName {
			cookie = ck
		}
	}
	rr2 := getJSON(t, h, "/api/settings", cookie)
	if rr2.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rr2.Code, rr2.Body.String())
	}
	var s SystemSettings
	_ = json.Unmarshal(rr2.Body.Bytes(), &s)
	// 默认是 small_vps_opt=false（来自 env 或 seed）
	if s.SmallVPSOpt {
		t.Errorf("SmallVPSOpt default = true, want false")
	}
	if s.MaxObjectSizeMB == 0 {
		t.Errorf("MaxObjectSizeMB = 0, want > 0")
	}
}

// TestSettings_Patch 改 small_vps_opt 后 get 应见。
func TestSettings_Patch(t *testing.T) {
	h, _ := settingsTestHandler(t)
	rr := postJSON(t, h, "/api/auth/login", LoginRequest{Username: "admin", Password: "admin1234"})
	var cookie *http.Cookie
	for _, ck := range rr.Result().Cookies() {
		if ck.Name == SessionCookieName {
			cookie = ck
		}
	}
	// 开 small_vps_opt
	tval := true
	rr2 := patchJSON(t, h, "/api/settings", SettingsPatch{SmallVPSOpt: &tval}, cookie)
	if rr2.Code != http.StatusOK {
		t.Fatalf("patch status = %d, want 200; body=%s", rr2.Code, rr2.Body.String())
	}
	var s SystemSettings
	_ = json.Unmarshal(rr2.Body.Bytes(), &s)
	if !s.SmallVPSOpt {
		t.Errorf("after patch SmallVPSOpt = false, want true")
	}
	// 再读
	rr3 := getJSON(t, h, "/api/settings", cookie)
	_ = json.Unmarshal(rr3.Body.Bytes(), &s)
	if !s.SmallVPSOpt {
		t.Errorf("after re-get SmallVPSOpt = false, want true")
	}
}

// TestSettings_Patch_InvalidValues 验证边界。
func TestSettings_Patch_InvalidValues(t *testing.T) {
	h, _ := settingsTestHandler(t)
	rr := postJSON(t, h, "/api/auth/login", LoginRequest{Username: "admin", Password: "admin1234"})
	var cookie *http.Cookie
	for _, ck := range rr.Result().Cookies() {
		if ck.Name == SessionCookieName {
			cookie = ck
		}
	}

	bad := 0
	rr2 := patchJSON(t, h, "/api/settings", SettingsPatch{CacheTotalGB: &bad}, cookie)
	if rr2.Code != http.StatusBadRequest {
		t.Errorf("CacheTotalGB=0 status = %d, want 400", rr2.Code)
	}

	// trigger < target 错
	trig, tgt := 30, 50
	rr3 := patchJSON(t, h, "/api/settings", SettingsPatch{CleanupTriggerPct: &trig, CleanupTargetPct: &tgt}, cookie)
	if rr3.Code != http.StatusBadRequest {
		t.Errorf("trigger<target status = %d, want 400", rr3.Code)
	}
}

// TestCleanup_DryRun 验证 dry-run 接口。
func TestCleanup_DryRun(t *testing.T) {
	h, db := settingsTestHandler(t)
	// 准备 cache_entries（LastAccessAt=0 会被 UpsertCacheEntry 改写成 now，
	// 这里显式设成 1 小时前）
	old := time.Now().Unix() - 3600
	for i := 0; i < 3; i++ {
		_, _ = db.UpsertCacheEntry(context.Background(), storage.CacheEntry{
			Registry: "dockerhub", Repository: "library/dry",
			Digest: "sha256:222222222222222222222222222222222222222222222222222222222222222" + string(rune('0'+i)),
			SizeBytes: 1024, LastAccessAt: old,
		})
	}
	rr := postJSON(t, h, "/api/auth/login", LoginRequest{Username: "admin", Password: "admin1234"})
	var cookie *http.Cookie
	for _, ck := range rr.Result().Cookies() {
		if ck.Name == SessionCookieName {
			cookie = ck
		}
	}
	// 把 lru task 的 threshold 改成 1s
	_, _ = db.SQLDB.ExecContext(context.Background(), `UPDATE cleanup_tasks SET threshold_seconds=1 WHERE task_name='default-lru'`)
	rr2 := postJSON(t, h, "/api/cleanup/tasks/1/dry-run", nil, cookie)
	if rr2.Code != http.StatusOK {
		t.Fatalf("dry-run status = %d, want 200; body=%s", rr2.Code, rr2.Body.String())
	}
	var r CleanupReport
	_ = json.Unmarshal(rr2.Body.Bytes(), &r)
	if r.FreedCount != 3 {
		t.Errorf("FreedCount = %d, want 3", r.FreedCount)
	}
	// 数据应还在
	var n int
	_ = db.SQLDB.QueryRowContext(context.Background(), `SELECT COUNT(*) FROM cache_entries`).Scan(&n)
	if n != 3 {
		t.Errorf("after dry-run, count = %d, want 3", n)
	}
}
