package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/cncachehub/server/internal/storage"
	"github.com/go-chi/chi/v5"
)

// masterKeyCipherForTest 实现 api.Cipher 的测试 stub（用固定 key）。
type masterKeyCipherForTest struct {
	key []byte
}

func (c *masterKeyCipherForTest) Encrypt(plaintext []byte) ([]byte, error) {
	// 测试 stub：简单用 key XOR（不是真加密，但能验证 handler 逻辑是否传对了）
	out := make([]byte, len(plaintext))
	for i, b := range plaintext {
		out[i] = b ^ c.key[i%len(c.key)]
	}
	return out, nil
}

func (c *masterKeyCipherForTest) Decrypt(ciphertext []byte) ([]byte, error) {
	out := make([]byte, len(ciphertext))
	for i, b := range ciphertext {
		out[i] = b ^ c.key[i%len(c.key)]
	}
	return out, nil
}

// withAdminCtx 模拟 requireAuth middleware 注入 user 到 context。
// registryPatchHandler 用 userFromContext(ctx) 拿 user，所以必须模拟。
func withAdminCtx(r *http.Request) *http.Request {
	ctx := context.WithValue(r.Context(), ctxKeyUser, AuthUser{
		ID:       1,
		Username: "admin",
		IsAdmin:  true,
	})
	return r.WithContext(ctx)
}

func registryTestRouter(t *testing.T) (http.Handler, *storage.DB) {
	t.Helper()
	dir := t.TempDir()
	ctx := context.Background()
	db, err := storage.Open(ctx, dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	opts := Options{
		AuthDB:                  db,
		ListRegistries:          db.ListAllUpstreams,
		SetRegistryEnabled:      db.SetUpstreamEnabled,
		SetRegistryCredentials:  db.SetUpstreamCredentials,
		CredentialCipher:        &masterKeyCipherForTest{key: []byte("test-key-32bytes-for-stub!!!!")},
		SessionUserRole: func(ctx context.Context, r *http.Request) (string, int64, error) {
			return "admin", 1, nil
		},
	}
	r := chi.NewRouter()
	r.Get("/api/registries", registriesListHandler(opts))
	r.Patch("/api/registries/{name}", registryPatchHandler(opts))
	return r, db
}

func TestRegistry_Get_NoCredsInitially(t *testing.T) {
	r, _ := registryTestRouter(t)
	req := httptest.NewRequest(http.MethodGet, "/api/registries", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("code = %d", rr.Code)
	}
	var body struct {
		Items []storage.Registry `json:"items"`
	}
	_ = json.Unmarshal(rr.Body.Bytes(), &body)
	if len(body.Items) == 0 {
		t.Fatal("expected seeded registries")
	}
	// 没有凭据
	for _, r := range body.Items {
		if r.HasPassword || r.HasToken || r.Username != "" {
			t.Errorf("registry %s has creds initially: %+v", r.Name, r)
		}
	}
}

func TestRegistry_Patch_SetCredentials(t *testing.T) {
	r, db := registryTestRouter(t)
	ctx := context.Background()

	body := `{"username": "alice", "password": "s3cret-pass", "token": "ghp_abc123"}`
	req := withAdminCtx(httptest.NewRequest(http.MethodPatch, "/api/registries/dockerhub", strings.NewReader(body)))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("code = %d, body=%s", rr.Code, rr.Body.String())
	}

	// 验证 DB 里的密文
	row, _ := db.GetUpstreamByName(ctx, "dockerhub")
	if row.Username != "alice" {
		t.Errorf("username = %q, want alice", row.Username)
	}
	if !row.HasPassword {
		t.Error("HasPassword should be true")
	}
	if !row.HasToken {
		t.Error("HasToken should be true")
	}
	// 密文不应该 == 明文
	if string(row.PasswordEnc) == "s3cret-pass" {
		t.Error("PasswordEnc stored as plaintext (not encrypted)")
	}
	if string(row.TokenEnc) == "ghp_abc123" {
		t.Error("TokenEnc stored as plaintext (not encrypted)")
	}
	// 响应里 tokenSet/hasPassword/hasToken 都应是 true
	var resp map[string]any
	_ = json.Unmarshal(rr.Body.Bytes(), &resp)
	if resp["username"] != "alice" {
		t.Errorf("response username = %v", resp["username"])
	}
	if resp["hasPassword"] != true {
		t.Errorf("response hasPassword = %v", resp["hasPassword"])
	}
}

func TestRegistry_Patch_GetShowsHasFlags(t *testing.T) {
	r, _ := registryTestRouter(t)

	// 1. 先 PATCH 设凭据
	patchBody := `{"password": "secret", "token": "tok123"}`
	req := withAdminCtx(httptest.NewRequest(http.MethodPatch, "/api/registries/dockerhub", strings.NewReader(patchBody)))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("PATCH code = %d", rr.Code)
	}

	// 2. GET 看 hasPassword/hasToken
	req2 := httptest.NewRequest(http.MethodGet, "/api/registries", nil)
	rr2 := httptest.NewRecorder()
	r.ServeHTTP(rr2, req2)
	var body struct {
		Items []storage.Registry `json:"items"`
	}
	_ = json.Unmarshal(rr2.Body.Bytes(), &body)

	var dockerhub *storage.Registry
	for i := range body.Items {
		if body.Items[i].Name == "dockerhub" {
			dockerhub = &body.Items[i]
			break
		}
	}
	if dockerhub == nil {
		t.Fatal("dockerhub not in list")
	}
	if !dockerhub.HasPassword {
		t.Error("HasPassword should be true in GET response")
	}
	if !dockerhub.HasToken {
		t.Error("HasToken should be true in GET response")
	}
	// 确保 GET 响应里没有泄露明文
	if dockerhub.PasswordEnc != nil {
		t.Error("PasswordEnc should be nil in JSON response (json:\"-\")")
	}
	if dockerhub.TokenEnc != nil {
		t.Error("TokenEnc should be nil in JSON response (json:\"-\")")
	}
}

func TestRegistry_Patch_ClearPassword(t *testing.T) {
	r, db := registryTestRouter(t)
	ctx := context.Background()

	// 1. 先设
	body1 := `{"password": "secret", "token": "tok"}`
	req := withAdminCtx(httptest.NewRequest(http.MethodPatch, "/api/registries/dockerhub", strings.NewReader(body1)))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("set code = %d", rr.Code)
	}

	// 2. clear password（保留 token）
	body2 := `{"clearPassword": true}`
	req2 := withAdminCtx(httptest.NewRequest(http.MethodPatch, "/api/registries/dockerhub", strings.NewReader(body2)))
	req2.Header.Set("Content-Type", "application/json")
	rr2 := httptest.NewRecorder()
	r.ServeHTTP(rr2, req2)
	if rr2.Code != http.StatusOK {
		t.Fatalf("clear code = %d", rr2.Code)
	}

	// 3. 验证 password 清了，token 还在
	row, _ := db.GetUpstreamByName(ctx, "dockerhub")
	if row.HasPassword {
		t.Error("HasPassword should be false after clear")
	}
	if !row.HasToken {
		t.Error("HasToken should still be true")
	}
}

func TestRegistry_Patch_OnlyEnabled(t *testing.T) {
	// 仅改 enabled 字段不应动 credentials
	r, db := registryTestRouter(t)
	ctx := context.Background()

	// 1. 先设凭据
	body1 := `{"password": "p", "token": "t"}`
	req := withAdminCtx(httptest.NewRequest(http.MethodPatch, "/api/registries/dockerhub", strings.NewReader(body1)))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("set code = %d", rr.Code)
	}
	pwBefore, _ := db.GetUpstreamByName(ctx, "dockerhub")
	pwEncBefore := string(pwBefore.PasswordEnc)
	tkEncBefore := string(pwBefore.TokenEnc)

	// 2. 仅改 enabled
	body2 := `{"enabled": false}`
	req2 := withAdminCtx(httptest.NewRequest(http.MethodPatch, "/api/registries/dockerhub", strings.NewReader(body2)))
	req2.Header.Set("Content-Type", "application/json")
	rr2 := httptest.NewRecorder()
	r.ServeHTTP(rr2, req2)
	if rr2.Code != http.StatusOK {
		t.Fatalf("enabled code = %d", rr2.Code)
	}

	// 3. 凭据没动
	pwAfter, _ := db.GetUpstreamByName(ctx, "dockerhub")
	if string(pwAfter.PasswordEnc) != pwEncBefore {
		t.Error("PasswordEnc changed when only enabled was patched")
	}
	if string(pwAfter.TokenEnc) != tkEncBefore {
		t.Error("TokenEnc changed when only enabled was patched")
	}
	if pwAfter.Enabled {
		t.Error("Enabled should be false")
	}
}

func TestRegistry_Patch_Noop400(t *testing.T) {
	r, _ := registryTestRouter(t)
	// 空 body
	req := withAdminCtx(httptest.NewRequest(http.MethodPatch, "/api/registries/dockerhub", strings.NewReader(`{}`)))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("empty patch should 400, got %d", rr.Code)
	}
}

func TestRegistry_Patch_NotFound(t *testing.T) {
	r, _ := registryTestRouter(t)
	body := `{"enabled": false}`
	req := withAdminCtx(httptest.NewRequest(http.MethodPatch, "/api/registries/nonexistent", strings.NewReader(body)))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusInternalServerError {
		// SetUpstreamEnabled 返回 ErrNotFound，但 handler 包了成 500
		t.Errorf("code = %d, want 500 (storage error)", rr.Code)
	}
}
