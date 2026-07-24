package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/cncachehub/server/internal/storage"
)

// buildAccessRouterWithRealDB 用真 storage.DB 构造 router — 避免 fake AuthDB 的 boilerplate。
func buildAccessRouterWithRealDB(t *testing.T) (http.Handler, *storage.DB) {
	t.Helper()
	dir := t.TempDir()
	ctx := context.Background()
	db, err := storage.Open(ctx, dir)
	if err != nil {
		t.Fatalf("storage.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	opts := Options{
		AuthDB:          db, // 满足 AuthDB 接口 + AccessControlDB 接口（通过 type assertion 走 GetMany/SetMany）
		SessionUserRole: func(ctx context.Context, r *http.Request) (string, int64, error) { return "admin", 1, nil },
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/access-control", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			accessControlGetHandler(opts)(w, r)
		case http.MethodPut:
			accessControlPutHandler(opts)(w, r)
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	})
	return mux, db
}

func TestAccessControl_Get_DefaultEmpty(t *testing.T) {
	r, _ := buildAccessRouterWithRealDB(t)

	req := httptest.NewRequest("GET", "/api/access-control", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("code = %d, want 200, body=%s", rr.Code, rr.Body.String())
	}
	var body AccessControlConfig
	if err := json.NewDecoder(rr.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Enabled {
		t.Error("default should be disabled")
	}
	if body.TokenSet {
		t.Error("default tokenSet should be false")
	}
	if body.LoopbackBypass != true {
		t.Error("default loopbackBypass should be true")
	}
}

func TestAccessControl_Put_EnableAndToken(t *testing.T) {
	r, db := buildAccessRouterWithRealDB(t)
	ctx := context.Background()

	body := `{"enabled": true, "token": "my-secret-123", "loopbackBypass": false}`
	req := httptest.NewRequest("PUT", "/api/access-control", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("code = %d, want 200, body=%s", rr.Code, rr.Body.String())
	}

	// 验证 DB 真写了
	if v := db.GetString(ctx, storage.SettingAccessControlEnabled, ""); v != "true" {
		t.Errorf("DB enabled = %q, want true", v)
	}
	if v := db.GetString(ctx, storage.SettingAccessControlToken, ""); v != "my-secret-123" {
		t.Errorf("DB token = %q", v)
	}
	if v := db.GetString(ctx, storage.SettingAccessControlLoopbackBypass, ""); v != "false" {
		t.Errorf("DB loopback = %q", v)
	}

	// 响应 token 应该是空（masked）
	var out AccessControlConfig
	_ = json.NewDecoder(rr.Body).Decode(&out)
	if out.Token != "" {
		t.Errorf("response token should be empty (masked), got %q", out.Token)
	}
	if !out.TokenSet {
		t.Error("response tokenSet should be true")
	}
}

func TestAccessControl_Put_InvalidCIDR(t *testing.T) {
	r, _ := buildAccessRouterWithRealDB(t)

	body := `{"ipWhitelist": ["10.0.0.0/8", "not-a-cidr"]}`
	req := httptest.NewRequest("PUT", "/api/access-control", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("code = %d, want 400", rr.Code)
	}
}

func TestAccessControl_Put_Noop(t *testing.T) {
	r, _ := buildAccessRouterWithRealDB(t)

	req := httptest.NewRequest("PUT", "/api/access-control", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("empty patch should 400, got %d", rr.Code)
	}
}

func TestAccessControl_Put_ValidCIDR(t *testing.T) {
	r, db := buildAccessRouterWithRealDB(t)
	ctx := context.Background()

	body := `{"ipWhitelist": ["10.0.0.0/8", "192.168.0.0/16", "172.16.0.0/12"]}`
	req := httptest.NewRequest("PUT", "/api/access-control", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("code = %d, want 200, body=%s", rr.Code, rr.Body.String())
	}
	if v := db.GetString(ctx, storage.SettingAccessControlIPWhitelist, ""); v != "10.0.0.0/8,192.168.0.0/16,172.16.0.0/12" {
		t.Errorf("ip whitelist = %q", v)
	}
}

// 防 io 警告
var _ = io.EOF
