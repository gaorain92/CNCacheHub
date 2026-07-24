package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/go-chi/chi/v5"
)

// withUser middleware 在 context 注入 user + 模拟 SessionUserRole 拿 role。
func withUser(role string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			isAdmin := role == "admin"
			ctx := context.WithValue(r.Context(), ctxKeyUser, AuthUser{
				ID:       1,
				Username: role,
				IsAdmin:  isAdmin,
			})
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// adminRouter 构造 chi router 含 RBAC-sensitive handler + fake auth role。
//
// 测试目标：非 admin 调用必须被 403 拦截。
func adminRouter(t *testing.T, path, method string, h http.HandlerFunc, role string) (http.Handler, *fakeAuthDB) {
	t.Helper()
	db := newFakeAuthDB(t)
	if _, err := db.CreateUser(context.Background(), "admin", "admin1234", true); err != nil {
		t.Fatal(err)
	}
	r := chi.NewRouter()
	// 模拟 requireAuth middleware 注入 user
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := context.WithValue(r.Context(), ctxKeyUser, AuthUser{
				ID:       1,
				Username: role,
				IsAdmin:  role == "admin",
			})
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	})
	r.Method(method, path, h)
	return r, db
}

func TestRBAC_CacheDelete_RequiresAdmin(t *testing.T) {
	opts := Options{
		DeleteCacheEntry: func(ctx context.Context, id int64) error { return nil },
		SessionUserRole:  func(ctx context.Context, r *http.Request) (string, int64, error) { return "user", 2, nil },
	}
	r := chi.NewRouter()
	r.Use(withUser("user"))
	r.Delete("/api/cache/entries/{id}", cacheDeleteHandler(opts))

	req := httptest.NewRequest(http.MethodDelete, "/api/cache/entries/1", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Errorf("non-admin should 403, got %d, body=%s", rr.Code, rr.Body.String())
	}
}

func TestRBAC_RunCleanup_RequiresAdmin(t *testing.T) {
	opts := Options{
		RunCleanupTask:  func(ctx context.Context, id int64) (CleanupReport, error) { return CleanupReport{}, nil },
		SessionUserRole: func(ctx context.Context, r *http.Request) (string, int64, error) { return "user", 2, nil },
	}
	r := chi.NewRouter()
	r.Use(withUser("user"))
	r.Post("/api/cleanup/tasks/{id}/run", runCleanupHandler(opts))

	req := httptest.NewRequest(http.MethodPost, "/api/cleanup/tasks/1/run", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Errorf("non-admin should 403, got %d, body=%s", rr.Code, rr.Body.String())
	}
}

func TestRBAC_CleanupDryRun_RequiresAdmin(t *testing.T) {
	opts := Options{
		DryRunCleanup:  func(ctx context.Context, id int64) (CleanupReport, error) { return CleanupReport{}, nil },
		SessionUserRole: func(ctx context.Context, r *http.Request) (string, int64, error) { return "user", 2, nil },
	}
	r := chi.NewRouter()
	r.Use(withUser("user"))
	r.Post("/api/cleanup/tasks/{id}/dry-run", cleanupDryRunHandler(opts))

	req := httptest.NewRequest(http.MethodPost, "/api/cleanup/tasks/1/dry-run", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Errorf("non-admin should 403, got %d, body=%s", rr.Code, rr.Body.String())
	}
}

func TestRBAC_CacheDelete_AdminAllowed(t *testing.T) {
	called := false
	opts := Options{
		DeleteCacheEntry: func(ctx context.Context, id int64) error {
			called = true
			return nil
		},
		SessionUserRole: func(ctx context.Context, r *http.Request) (string, int64, error) { return "admin", 1, nil },
	}
	r := chi.NewRouter()
	r.Use(withUser("admin"))
	r.Delete("/api/cache/entries/{id}", cacheDeleteHandler(opts))

	req := httptest.NewRequest(http.MethodDelete, "/api/cache/entries/42", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Errorf("admin should 200, got %d, body=%s", rr.Code, rr.Body.String())
	}
	if !called {
		t.Error("DeleteCacheEntry adapter not called")
	}
	var body map[string]any
	_ = json.Unmarshal(rr.Body.Bytes(), &body)
	if id, _ := body["id"].(float64); int64(id) != 42 {
		t.Errorf("id = %v, want 42", body["id"])
	}
}

// 防止 strconv 警告
var _ = strconv.Itoa