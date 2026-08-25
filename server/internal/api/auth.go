// Package api: 鉴权（PRD §9.7.1 + §14.1）。
//
// 路由：
//   - POST   /api/auth/login          公开
//   - POST   /api/auth/logout         公开（无 session 也 200）
//   - GET    /api/auth/me             公开（未登录 200, authenticated=false）
//   - POST   /api/auth/change-password 需要登录
//   - GET    /api/auth/init-status    公开（首次启动判定用）
//   - POST   /api/auth/init           公开（仅当无 admin 时）
//
// Cookie：cnsid=HttpOnly; SameSite=Lax; 7 天。
package api

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/cncachehub/server/internal/clientip"
	"github.com/cncachehub/server/internal/storage"
	"golang.org/x/crypto/bcrypt"
)

// SessionCookieName 控制台登录 cookie。
const SessionCookieName = "cnsid"

// SessionTTL 控制台 session 有效期（7 天）。
const SessionTTL = 7 * 24 * time.Hour

// MaxLoginFailures 连续失败阈值（PRD 12.1 — phase 1.2 完整实现）。
const MaxLoginFailures = 5

// 业务异常。
var (
	ErrUnauthorized     = errors.New("unauthorized")
	ErrAccountDisabled  = errors.New("account disabled")
	ErrInvalidInit      = errors.New("invalid init request")
	ErrAlreadyInit      = errors.New("already initialized")
)

// AuthUser / AuthSession / AuthAudit 用 storage 包的具体类型。
//
// api 不重复定义，避免字段漂移；json tag 由 storage 包统一管理。
type (
	AuthUser    = storage.User
	AuthSession = storage.Session
	AuthAudit   = storage.AuditLog
)

// AuthDB 接口让 main 注入 storage.DB。
type AuthDB interface {
	CountUsers(ctx context.Context) (int, error)
	CreateUser(ctx context.Context, username, plainPassword string, isAdmin bool) (*AuthUser, error)
	GetUserByUsername(ctx context.Context, username string) (*AuthUser, error)
	GetUserByID(ctx context.Context, id int64) (*AuthUser, error)
	UpdateUserPassword(ctx context.Context, id int64, newPlain string) error
	UpdateUserLastLogin(ctx context.Context, id int64) error
	CreateSession(ctx context.Context, userID int64, ip, userAgent string, ttl time.Duration) (*AuthSession, error)
	GetSession(ctx context.Context, token string) (*AuthSession, error)
	TouchSession(ctx context.Context, token string) error
	DeleteSession(ctx context.Context, token string) error
	WriteAudit(ctx context.Context, e AuthAudit) error
}

// ============================================================================
// DTO（API 入参出参）
// ============================================================================

// LoginRequest 是 POST /api/auth/login body。
type LoginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// LoginResponse 是登录成功返回。
type LoginResponse struct {
	User      AuthUser    `json:"user"`
	Session   AuthSession `json:"session"`
	ExpiresAt int64       `json:"expiresAt"`
}

// MeResponse 是 /api/auth/me 返回。
type MeResponse struct {
	User               *AuthUser `json:"user,omitempty"`
	Authenticated      bool      `json:"authenticated"`
	MustChangePassword bool      `json:"mustChangePassword"`
	// InitRequired: true 表示需要走初始化向导（无任何 admin）
	InitRequired bool `json:"initRequired"`
}

// ChangePasswordRequest 是 POST /api/auth/change-password body。
type ChangePasswordRequest struct {
	OldPassword string `json:"oldPassword"`
	NewPassword string `json:"newPassword"`
}

// InitRequest 是 POST /api/auth/init body。
type InitRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// InitStatusResponse 是 GET /api/auth/init-status 返回。
type InitStatusResponse struct {
	Initialized bool `json:"initialized"`
	UserCount   int  `json:"userCount"`
}

// ============================================================================
// Handlers
// ============================================================================

// loginHandler POST /api/auth/login
//
// body: {"username": "...", "password": "..."}
// resp 200: LoginResponse
// resp 401: invalid credentials（不区分用户名/密码错误，避免枚举）
// resp 423: account disabled
func loginHandler(opts Options) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if opts.AuthDB == nil {
			writeError(w, http.StatusServiceUnavailable, "auth_unavailable", "auth backend not configured")
			return
		}
		var req LoginRequest
		if !decodeJSONBody(w, r, &req) {
			return
		}
		username := strings.TrimSpace(req.Username)
		if username == "" || req.Password == "" {
			writeError(w, http.StatusBadRequest, "missing_credentials", "username and password required")
			return
		}
		ip := clientIP(r)
		ua := r.UserAgent()

		u, err := opts.AuthDB.GetUserByUsername(r.Context(), username)
		if err != nil || u == nil {
			_ = opts.AuthDB.WriteAudit(r.Context(), AuthAudit{UserID: 0, Action: "login", IP: ip, UserAgent: ua, Status: "fail", Details: "user_not_found"})
			writeError(w, http.StatusUnauthorized, "invalid_credentials", "invalid username or password")
			return
		}
		if u.Disabled {
			_ = opts.AuthDB.WriteAudit(r.Context(), AuthAudit{UserID: u.ID, Action: "login", IP: ip, UserAgent: ua, Status: "fail", Details: "disabled"})
			writeError(w, http.StatusLocked, "account_disabled", "account is disabled")
			return
		}
		if !verifyBcrypt(u.PasswordHash, req.Password) {
			_ = opts.AuthDB.WriteAudit(r.Context(), AuthAudit{UserID: u.ID, Action: "login", IP: ip, UserAgent: ua, Status: "fail", Details: "bad_password"})
			writeError(w, http.StatusUnauthorized, "invalid_credentials", "invalid username or password")
			return
		}
		sess, err := opts.AuthDB.CreateSession(r.Context(), u.ID, ip, ua, SessionTTL)
		if err != nil {
			writeInternalErr(w, r, "session_create_failed", err)
			return
		}
		_ = opts.AuthDB.UpdateUserLastLogin(r.Context(), u.ID)
		_ = opts.AuthDB.WriteAudit(r.Context(), AuthAudit{UserID: u.ID, Action: "login", IP: ip, UserAgent: ua, Status: "ok"})

		// 写 cookie
		setSessionCookie(w, sess.Token, SessionTTL)

		// 清 PasswordHash 不外露（json:"-" 已经在 storage.User 上做了，验证一下）
		writeJSON(w, http.StatusOK, LoginResponse{User: *u, Session: *sess, ExpiresAt: sess.ExpiresAt})
	}
}

// logoutHandler POST /api/auth/logout
func logoutHandler(opts Options) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		c, err := r.Cookie(SessionCookieName)
		if err == nil && c.Value != "" && opts.AuthDB != nil {
			_ = opts.AuthDB.DeleteSession(r.Context(), c.Value)
		}
		clearSessionCookie(w)
		writeJSON(w, http.StatusOK, map[string]any{"loggedOut": true})
	}
}

// meHandler GET /api/auth/me
func meHandler(opts Options) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if opts.AuthDB == nil {
			writeJSON(w, http.StatusOK, MeResponse{Authenticated: false})
			return
		}
		n, _ := opts.AuthDB.CountUsers(r.Context())
		initRequired := n == 0

		tok := sessionTokenFromRequest(r)
		if tok == "" {
			writeJSON(w, http.StatusOK, MeResponse{Authenticated: false, InitRequired: initRequired})
			return
		}
		sess, err := opts.AuthDB.GetSession(r.Context(), tok)
		if err != nil || sess == nil {
			clearSessionCookie(w)
			writeJSON(w, http.StatusOK, MeResponse{Authenticated: false, InitRequired: initRequired})
			return
		}
		u, err := opts.AuthDB.GetUserByID(r.Context(), sess.UserID)
		if err != nil || u == nil {
			clearSessionCookie(w)
			writeJSON(w, http.StatusOK, MeResponse{Authenticated: false, InitRequired: initRequired})
			return
		}
		_ = opts.AuthDB.TouchSession(r.Context(), tok)
		writeJSON(w, http.StatusOK, MeResponse{
			User:               u,
			Authenticated:      true,
			MustChangePassword: u.MustChangePassword,
			InitRequired:       initRequired,
		})
	}
}

// changePasswordHandler POST /api/auth/change-password
func changePasswordHandler(opts Options) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		uid, ok := userIDFromContext(r.Context())
		if !ok {
			writeError(w, http.StatusUnauthorized, "unauthorized", "login required")
			return
		}
		var req ChangePasswordRequest
		if !decodeJSONBody(w, r, &req) {
			return
		}
		if len(req.NewPassword) < 8 {
			writeError(w, http.StatusBadRequest, "weak_password", "new password must be at least 8 characters")
			return
		}
		ip := clientIP(r)
		ua := r.UserAgent()

		u, err := opts.AuthDB.GetUserByID(r.Context(), uid)
		if err != nil || u == nil {
			writeError(w, http.StatusUnauthorized, "user_not_found", "user not found")
			return
		}
		if !verifyBcrypt(u.PasswordHash, req.OldPassword) {
			_ = opts.AuthDB.WriteAudit(r.Context(), AuthAudit{UserID: uid, Action: "change_password", IP: ip, UserAgent: ua, Status: "fail", Details: "bad_old"})
			writeError(w, http.StatusUnauthorized, "invalid_old_password", "old password is incorrect")
			return
		}
		if req.OldPassword == req.NewPassword {
			writeError(w, http.StatusBadRequest, "same_password", "new password must differ from old")
			return
		}
		if err := opts.AuthDB.UpdateUserPassword(r.Context(), uid, req.NewPassword); err != nil {
			writeInternalErr(w, r, "password_update_failed", err)
			return
		}
		_ = opts.AuthDB.WriteAudit(r.Context(), AuthAudit{UserID: uid, Action: "change_password", IP: ip, UserAgent: ua, Status: "ok"})
		writeJSON(w, http.StatusOK, map[string]any{"changed": true})
	}
}

// initStatusHandler GET /api/auth/init-status
func initStatusHandler(opts Options) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if opts.AuthDB == nil {
			writeError(w, http.StatusServiceUnavailable, "auth_unavailable", "auth backend not configured")
			return
		}
		n, err := opts.AuthDB.CountUsers(r.Context())
		if err != nil {
			writeInternalErr(w, r, "count_users_failed", err)
			return
		}
		writeJSON(w, http.StatusOK, InitStatusResponse{Initialized: n > 0, UserCount: n})
	}
}

// initHandler POST /api/auth/init
func initHandler(opts Options) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if opts.AuthDB == nil {
			writeError(w, http.StatusServiceUnavailable, "auth_unavailable", "auth backend not configured")
			return
		}
		var req InitRequest
		if !decodeJSONBody(w, r, &req) {
			return
		}
		username := strings.TrimSpace(req.Username)
		if username == "" || len(req.Password) < 8 {
			writeError(w, http.StatusBadRequest, "invalid_init", "username required; password must be at least 8 characters")
			return
		}
		n, _ := opts.AuthDB.CountUsers(r.Context())
		if n > 0 {
			writeError(w, http.StatusForbidden, "already_initialized", "admin user already exists")
			return
		}
		u, err := opts.AuthDB.CreateUser(r.Context(), username, req.Password, true)
		if err != nil {
			// race：另一个 init 已经创建。unique constraint → 视为已初始化。
			if isUniqueViolation(err) {
				writeError(w, http.StatusForbidden, "already_initialized", "admin user already exists")
				return
			}
			writeInternalErr(w, r, "user_create_failed", err)
			return
		}
		ip := clientIP(r)
		ua := r.UserAgent()
		_ = opts.AuthDB.WriteAudit(r.Context(), AuthAudit{UserID: u.ID, Action: "init", IP: ip, UserAgent: ua, Status: "ok", Details: "first_admin"})
		// 自动登录
		sess, err := opts.AuthDB.CreateSession(r.Context(), u.ID, ip, ua, SessionTTL)
		if err != nil {
			writeInternalErr(w, r, "session_create_failed", err)
			return
		}
		setSessionCookie(w, sess.Token, SessionTTL)
		writeJSON(w, http.StatusOK, LoginResponse{User: *u, Session: *sess, ExpiresAt: sess.ExpiresAt})
	}
}

// isUniqueViolation 判断 err 是否是 unique constraint 冲突。
//
// modernc.org/sqlite 把 sqlite 错误编码到 message 里，常见：
//   - "UNIQUE constraint failed: users.username"
//   - "constraint failed: UNIQUE"
// 简化判断：包含 "UNIQUE" 即可。
func isUniqueViolation(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "UNIQUE") || strings.Contains(msg, "unique")
}

// ============================================================================
// Middleware
// ============================================================================

// requireAuth 验证 cookie → session → user，注入到 ctx。
//
// 跳过路径：
//   - /api/auth/* 自身
//   - /api/healthz, /api/version（健康检查 + 版本，外部探活用）
//   - /v2/*（registry 公开，PRD §9.7.2 独立按 IP/token 鉴权）
//
// 其它 /api/* 都要登录。
func requireAuth(opts Options) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			path := r.URL.Path
			if !needsAuth(path) {
				next.ServeHTTP(w, r)
				return
			}
			if opts.AuthDB == nil {
				writeError(w, http.StatusServiceUnavailable, "auth_unavailable", "auth backend not configured")
				return
			}
			tok := sessionTokenFromRequest(r)
			if tok == "" {
				writeError(w, http.StatusUnauthorized, "unauthorized", "login required")
				return
			}
			sess, err := opts.AuthDB.GetSession(r.Context(), tok)
			if err != nil || sess == nil {
				clearSessionCookie(w)
				writeError(w, http.StatusUnauthorized, "session_expired", "session expired or invalid")
				return
			}
			if sess.ExpiresAt < time.Now().Unix() {
				_ = opts.AuthDB.DeleteSession(r.Context(), tok)
				clearSessionCookie(w)
				writeError(w, http.StatusUnauthorized, "session_expired", "session expired")
				return
			}
			u, err := opts.AuthDB.GetUserByID(r.Context(), sess.UserID)
			if err != nil || u == nil {
				clearSessionCookie(w)
				writeError(w, http.StatusUnauthorized, "user_missing", "user no longer exists")
				return
			}
			if u.Disabled {
				_ = opts.AuthDB.DeleteSession(r.Context(), tok)
				clearSessionCookie(w)
				writeError(w, http.StatusLocked, "account_disabled", "account is disabled")
				return
			}
			_ = opts.AuthDB.TouchSession(r.Context(), tok)
			ctx := context.WithValue(r.Context(), ctxKeyUserID, u.ID)
			ctx = context.WithValue(ctx, ctxKeyUser, *u)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// needsAuth 判断路径是否需要登录。
//
// 公开（白名单）：
//   - /api/auth/login, /api/auth/logout, /api/auth/me
//   - /api/auth/init, /api/auth/init-status（首次启动前）
//   - /api/healthz, /api/version
//   - /api/client-config (公开，daemon 拉配置)
//   - /v2/* （registry 公开，PRD §9.7.2 独立按 IP/token 鉴权）
//
// 必须登录：
//   - 其它 /api/* 包括 /api/auth/change-password
func needsAuth(path string) bool {
	// 显式公开列表（避免误把 /api/auth/change-password 也当公开）
	for _, p := range []string{
		"/api/auth/login",
		"/api/auth/logout",
		"/api/auth/me",
		"/api/auth/init",
		"/api/auth/init-status",
		"/api/healthz",
		"/api/version",
		"/api/client-config",
		"/api/client-config/bundle",
	} {
		if p == path {
			return false
		}
	}
	// 公开前缀
	for _, prefix := range []string{"/v2/"} {
		if path == prefix || strings.HasPrefix(path, prefix) {
			return false
		}
	}
	if path == "/v2" {
		return false
	}
	// 其它 /api/* 都要登录
	return strings.HasPrefix(path, "/api/")
}

// ============================================================================
// Helpers
// ============================================================================

type ctxKey int

const (
	ctxKeyUserID ctxKey = iota + 1
	ctxKeyUser
)

func userIDFromContext(ctx context.Context) (int64, bool) {
	v := ctx.Value(ctxKeyUserID)
	if v == nil {
		return 0, false
	}
	id, ok := v.(int64)
	return id, ok
}

func userFromContext(ctx context.Context) (*AuthUser, bool) {
	v := ctx.Value(ctxKeyUser)
	if v == nil {
		return nil, false
	}
	u, ok := v.(AuthUser)
	if !ok {
		return nil, false
	}
	return &u, true
}

func setSessionCookie(w http.ResponseWriter, token string, ttl time.Duration) {
	http.SetCookie(w, &http.Cookie{
		Name:     SessionCookieName,
		Value:    token,
		Path:     "/",
		MaxAge:   int(ttl.Seconds()),
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		// Secure: true, // phase 1.2 启用 HTTPS 时再开
	})
}

func clearSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     SessionCookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
}

func sessionTokenFromRequest(r *http.Request) string {
	c, err := r.Cookie(SessionCookieName)
	if err == nil && c.Value != "" {
		return c.Value
	}
	auth := r.Header.Get("Authorization")
	if strings.HasPrefix(auth, "Bearer ") {
		return strings.TrimPrefix(auth, "Bearer ")
	}
	return ""
}

func clientIP(r *http.Request) string {
	// 安全提取：只信任 trusted proxy（默认 loopback + RFC1918）设置的 XFF。
	// 直接连到 :8082 的攻击者无法伪造 IP 绕过 rate limit / access control。
	return clientip.Real(r)
}

// verifyBcrypt 校验 bcrypt hash vs 明文。
func verifyBcrypt(hash, plain string) bool {
	if hash == "" {
		return false
	}
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(plain)) == nil
}
