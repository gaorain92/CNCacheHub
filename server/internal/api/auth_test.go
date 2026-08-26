package api

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cncachehub/server/internal/storage"
	"golang.org/x/crypto/bcrypt"
)

// fakeAuthDB 是 AuthDB 的内存 fake。
//
// 注意：直接复用 storage 包的真实实现（embedded），保证行为一致。
// 这里只为了解耦：测试用真实 storage.Open 但走临时目录。
type fakeAuthDB struct {
	*storage.DB
}

func newFakeAuthDB(t *testing.T) *fakeAuthDB {
	t.Helper()
	dir := t.TempDir()
	db, err := storage.Open(context.Background(), dir)
	if err != nil {
		t.Fatalf("storage.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return &fakeAuthDB{DB: db}
}

// newAuthedHandler 用真实 storage 起一个完整 router。
func newAuthedHandler(t *testing.T) (http.Handler, *fakeAuthDB) {
	t.Helper()
	db := newFakeAuthDB(t)
	// 创建一个 admin 便于测试登录
	_, err := db.CreateUser(context.Background(), "admin", "admin1234", true)
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	return NewRouter(Options{AuthDB: db}), db
}

func postJSON(t *testing.T, h http.Handler, path string, body any, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&buf).Encode(body); err != nil {
			t.Fatalf("encode: %v", err)
		}
	}
	req := httptest.NewRequest(http.MethodPost, path, &buf)
	req.Header.Set("Content-Type", "application/json")
	for _, c := range cookies {
		req.AddCookie(c)
	}
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

func getJSON(t *testing.T, h http.Handler, path string, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	for _, c := range cookies {
		req.AddCookie(c)
	}
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

// TestInitStatus_BeforeInit 验证未初始化时 init-status 返回 initialized=false。
func TestInitStatus_BeforeInit(t *testing.T) {
	h := NewRouter(Options{AuthDB: newFakeAuthDB(t)})
	rr := getJSON(t, h, "/api/auth/init-status")
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	var body InitStatusResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if body.Initialized {
		t.Errorf("Initialized = true, want false")
	}
	if body.UserCount != 0 {
		t.Errorf("UserCount = %d, want 0", body.UserCount)
	}
}

// TestInit_CreatesFirstAdmin 验证 init 创建第一个 admin 并自动登录。
func TestInit_CreatesFirstAdmin(t *testing.T) {
	db := newFakeAuthDB(t)
	h := NewRouter(Options{AuthDB: db})

	rr := postJSON(t, h, "/api/auth/init", InitRequest{Username: "root", Password: "root1234"})
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	// 应有 Set-Cookie cnsid
	var cookie *http.Cookie
	for _, ck := range rr.Result().Cookies() {
		if ck.Name == SessionCookieName {
			cookie = ck
			break
		}
	}
	if cookie == nil {
		t.Fatalf("missing %s cookie", SessionCookieName)
	}
	if cookie.Value == "" {
		t.Errorf("cookie value empty")
	}
	if !cookie.HttpOnly {
		t.Errorf("cookie HttpOnly = false, want true")
	}
	// DB 应有 1 个用户
	n, _ := db.CountUsers(context.Background())
	if n != 1 {
		t.Errorf("user count = %d, want 1", n)
	}
}

// TestInit_RejectsSecond 验证已有 admin 时 init 返回 403。
func TestInit_RejectsSecond(t *testing.T) {
	db := newFakeAuthDB(t)
	_, _ = db.CreateUser(context.Background(), "admin", "admin1234", true)
	h := NewRouter(Options{AuthDB: db})

	rr := postJSON(t, h, "/api/auth/init", InitRequest{Username: "root2", Password: "root1234"})
	if rr.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403; body=%s", rr.Code, rr.Body.String())
	}
}

// TestInit_WeakPassword 验证密码 < 8 字符拒绝。
func TestInit_WeakPassword(t *testing.T) {
	h := NewRouter(Options{AuthDB: newFakeAuthDB(t)})
	rr := postJSON(t, h, "/api/auth/init", InitRequest{Username: "root", Password: "short"})
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rr.Code)
	}
}

// TestLogin_Valid 验证正常登录流程。
func TestLogin_Valid(t *testing.T) {
	h, _ := newAuthedHandler(t)
	rr := postJSON(t, h, "/api/auth/login", LoginRequest{Username: "admin", Password: "admin1234"})
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	var resp LoginResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.User.Username != "admin" {
		t.Errorf("user = %q, want admin", resp.User.Username)
	}
	if !resp.User.IsAdmin {
		t.Errorf("isAdmin = false, want true")
	}
	if resp.User.PasswordHash != "" {
		t.Errorf("PasswordHash leaked: %q", resp.User.PasswordHash)
	}
	if resp.Session.Token == "" {
		t.Errorf("session token empty")
	}
	if resp.ExpiresAt <= time.Now().Unix() {
		t.Errorf("ExpiresAt = %d not in future", resp.ExpiresAt)
	}
	// cookie 写入
	var found bool
	for _, ck := range rr.Result().Cookies() {
		if ck.Name == SessionCookieName && ck.Value == resp.Session.Token {
			found = true
		}
	}
	if !found {
		t.Errorf("session cookie not set or value mismatch")
	}
}

// TestLogin_WrongPassword 401 + 不暴露用户存在。
func TestLogin_WrongPassword(t *testing.T) {
	h, _ := newAuthedHandler(t)
	rr := postJSON(t, h, "/api/auth/login", LoginRequest{Username: "admin", Password: "wrong"})
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rr.Code)
	}
	var body map[string]any
	_ = json.Unmarshal(rr.Body.Bytes(), &body)
	errObj, _ := body["error"].(map[string]any)
	if errObj["code"] != "invalid_credentials" {
		t.Errorf("code = %v, want invalid_credentials", errObj["code"])
	}
}

// TestLogin_UnknownUser 同样返回 401 invalid_credentials。
func TestLogin_UnknownUser(t *testing.T) {
	h, _ := newAuthedHandler(t)
	rr := postJSON(t, h, "/api/auth/login", LoginRequest{Username: "ghost", Password: "whatever"})
	if rr.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", rr.Code)
	}
	var body map[string]any
	_ = json.Unmarshal(rr.Body.Bytes(), &body)
	errObj, _ := body["error"].(map[string]any)
	if errObj["code"] != "invalid_credentials" {
		t.Errorf("code = %v, want invalid_credentials (no enum)", errObj["code"])
	}
}

// TestMe_Unauthenticated 200 + authenticated=false。
func TestMe_Unauthenticated(t *testing.T) {
	h, _ := newAuthedHandler(t)
	rr := getJSON(t, h, "/api/auth/me")
	if rr.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rr.Code)
	}
	var body MeResponse
	_ = json.Unmarshal(rr.Body.Bytes(), &body)
	if body.Authenticated {
		t.Errorf("authenticated = true, want false")
	}
	if body.InitRequired {
		t.Errorf("initRequired = true, want false (admin exists)")
	}
}

// TestMe_Authenticated 200 + user info。
func TestMe_Authenticated(t *testing.T) {
	h, _ := newAuthedHandler(t)
	// 登录拿 cookie
	rr := postJSON(t, h, "/api/auth/login", LoginRequest{Username: "admin", Password: "admin1234"})
	if rr.Code != http.StatusOK {
		t.Fatalf("login failed: %d", rr.Code)
	}
	var cookie *http.Cookie
	for _, ck := range rr.Result().Cookies() {
		if ck.Name == SessionCookieName {
			cookie = ck
		}
	}
	// 带 cookie 调 /me
	rr2 := getJSON(t, h, "/api/auth/me", cookie)
	if rr2.Code != http.StatusOK {
		t.Fatalf("me status = %d, want 200", rr2.Code)
	}
	var body MeResponse
	_ = json.Unmarshal(rr2.Body.Bytes(), &body)
	if !body.Authenticated {
		t.Errorf("authenticated = false, want true")
	}
	if body.User == nil || body.User.Username != "admin" {
		t.Errorf("user = %+v, want admin", body.User)
	}
}

// TestLogout_ClearsCookie + 调 logout 后 session 失效。
func TestLogout(t *testing.T) {
	h, db := newAuthedHandler(t)
	rr := postJSON(t, h, "/api/auth/login", LoginRequest{Username: "admin", Password: "admin1234"})
	var cookie *http.Cookie
	for _, ck := range rr.Result().Cookies() {
		if ck.Name == SessionCookieName {
			cookie = ck
		}
	}
	// logout
	rr2 := postJSON(t, h, "/api/auth/logout", nil, cookie)
	if rr2.Code != http.StatusOK {
		t.Errorf("logout status = %d, want 200", rr2.Code)
	}
	// 验证 session 已删
	if _, err := db.GetSession(context.Background(), cookie.Value); err != storage.ErrNotFound {
		t.Errorf("session should be gone, got err=%v", err)
	}
}

// TestChangePassword 改密后旧密码失效。
func TestChangePassword(t *testing.T) {
	h, _ := newAuthedHandler(t)
	rr := postJSON(t, h, "/api/auth/login", LoginRequest{Username: "admin", Password: "admin1234"})
	if rr.Code != http.StatusOK {
		t.Fatalf("login failed: %d %s", rr.Code, rr.Body.String())
	}
	var cookie *http.Cookie
	for _, ck := range rr.Result().Cookies() {
		if ck.Name == SessionCookieName {
			cookie = ck
		}
	}
	if cookie == nil {
		t.Fatal("no session cookie")
	}
	t.Logf("got cookie: name=%s value=%s...", cookie.Name, cookie.Value[:12])
	// 先 /me 验证 cookie 工作
	rr0 := getJSON(t, h, "/api/auth/me", cookie)
	t.Logf("/me with cookie: status=%d body=%s", rr0.Code, rr0.Body.String())
	// 改密
	rr2 := postJSON(t, h, "/api/auth/change-password",
		ChangePasswordRequest{OldPassword: "admin1234", NewPassword: "newtestpw"},
		cookie)
	if rr2.Code != http.StatusOK {
		t.Fatalf("change-password status = %d, want 200; body=%s", rr2.Code, rr2.Body.String())
	}
	// 用旧密码登录应失败
	rr3 := postJSON(t, h, "/api/auth/login", LoginRequest{Username: "admin", Password: "admin1234"})
	if rr3.Code != http.StatusUnauthorized {
		t.Errorf("old password login = %d, want 401", rr3.Code)
	}
	// 用新密码登录应成功
	rr4 := postJSON(t, h, "/api/auth/login", LoginRequest{Username: "admin", Password: "newtestpw"})
	if rr4.Code != http.StatusOK {
		t.Errorf("new password login = %d, want 200", rr4.Code)
	}
}

// TestChangePassword_WrongOld 旧密码错。
func TestChangePassword_WrongOld(t *testing.T) {
	h, _ := newAuthedHandler(t)
	rr := postJSON(t, h, "/api/auth/login", LoginRequest{Username: "admin", Password: "admin1234"})
	var cookie *http.Cookie
	for _, ck := range rr.Result().Cookies() {
		if ck.Name == SessionCookieName {
			cookie = ck
		}
	}
	rr2 := postJSON(t, h, "/api/auth/change-password",
		ChangePasswordRequest{OldPassword: "wrong", NewPassword: "newtestpw"},
		cookie)
	if rr2.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", rr2.Code)
	}
}

// ============================================================================
// requireAuth middleware 测试
// ============================================================================

// TestRequireAuth_DashboardRequiresAuth 验证未登录访问 /api/dashboard/summary 返回 401。
func TestRequireAuth_DashboardRequiresAuth(t *testing.T) {
	h, _ := newAuthedHandler(t)
	rr := getJSON(t, h, "/api/dashboard/summary")
	if rr.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", rr.Code)
	}
}

// TestRequireAuth_DashboardWithCookie 登录后能访问 dashboard。
func TestRequireAuth_DashboardWithCookie(t *testing.T) {
	h, _ := newAuthedHandler(t)
	rr := postJSON(t, h, "/api/auth/login", LoginRequest{Username: "admin", Password: "admin1234"})
	var cookie *http.Cookie
	for _, ck := range rr.Result().Cookies() {
		if ck.Name == SessionCookieName {
			cookie = ck
		}
	}
	// 没 opts.GetDashboardSummary 注入但 middleware 通过 — handler 返回 503，不应是 401
	rr2 := getJSON(t, h, "/api/dashboard/summary", cookie)
	if rr2.Code == http.StatusUnauthorized {
		t.Errorf("status = 401, should be allowed (not 503 from missing deps)")
	}
}

// TestRequireAuth_V2Public 验证 /v2/* 不需要登录。
func TestRequireAuth_V2Public(t *testing.T) {
	h, _ := newAuthedHandler(t)
	rr := getJSON(t, h, "/v2/")
	// 没 proxy handler 注入，应 503
	if rr.Code == http.StatusUnauthorized {
		t.Errorf("/v2/ returned 401, want public (503 or 200)")
	}
}

// TestRequireAuth_InvalidSession 非法 session token 返回 401。
func TestRequireAuth_InvalidSession(t *testing.T) {
	h, _ := newAuthedHandler(t)
	bad := &http.Cookie{Name: SessionCookieName, Value: "not-a-real-token"}
	rr := getJSON(t, h, "/api/dashboard/summary", bad)
	if rr.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", rr.Code)
	}
}

// TestPasswordHash_NotExposed 验证 storage.User 的 PasswordHash json:"-"。
func TestPasswordHash_NotExposed(t *testing.T) {
	hash, err := bcrypt.GenerateFromPassword([]byte("secret"), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	u := storage.User{ID: 1, Username: "u", PasswordHash: string(hash)}
	b, _ := json.Marshal(u)
	if strings.Contains(string(b), "secret") {
		t.Errorf("plain password leaked: %s", b)
	}
	if strings.Contains(string(b), string(hash)) {
		t.Errorf("bcrypt hash leaked: %s", b)
	}
}

// TestConcurrent_Init 验证不会重复 init（race）。
func TestConcurrent_Init(t *testing.T) {
	db := newFakeAuthDB(t)
	h := NewRouter(Options{AuthDB: db})
	var wg sync.WaitGroup
	codes := make([]int, 5)
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			rr := postJSON(t, h, "/api/auth/init", InitRequest{Username: "root", Password: "root1234"})
			codes[i] = rr.Code
		}(i)
	}
	wg.Wait()
	// 至少一个 200，其它 403（already_initialized）
	var ok, forbid int
	for _, c := range codes {
		switch c {
		case http.StatusOK:
			ok++
		case http.StatusForbidden:
			forbid++
		default:
			t.Errorf("unexpected status: %d", c)
		}
	}
	if ok != 1 {
		t.Errorf("ok count = %d, want 1", ok)
	}
	if forbid != 4 {
		t.Errorf("forbid count = %d, want 4", forbid)
	}
}

// 防止 io 引用未用
var _ = io.EOF
