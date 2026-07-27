package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/cncachehub/server/internal/ratelimit"
	"github.com/go-chi/chi/v5"
)

// TestRateLimitMiddleware_AllowsUnderLimit 正常请求通过。
func TestRateLimitMiddleware_AllowsUnderLimit(t *testing.T) {
	lim := ratelimit.NewLimiter(5, 1, time.Minute)
	r := chi.NewRouter()
	r.Use(rateLimitMiddleware(lim, nil))
	r.Get("/test", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"ok": "true"})
	})

	for i := 0; i < 5; i++ {
		req := httptest.NewRequest("GET", "/test", nil)
		req.RemoteAddr = "1.2.3.4:1234"
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Errorf("request %d: status = %d, want 200", i, rec.Code)
		}
	}
}

// TestRateLimitMiddleware_BlocksOverLimit 超限返回 429。
func TestRateLimitMiddleware_BlocksOverLimit(t *testing.T) {
	lim := ratelimit.NewLimiter(3, 0, time.Minute) // 0 refill = 不补充
	r := chi.NewRouter()
	r.Use(rateLimitMiddleware(lim, nil))
	r.Get("/test", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"ok": "true"})
	})

	// 先用完 3 个 token
	for i := 0; i < 3; i++ {
		req := httptest.NewRequest("GET", "/test", nil)
		req.RemoteAddr = "10.0.0.1:5555"
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("pre-fill request %d: status = %d, want 200", i, rec.Code)
		}
	}

	// 第 4 个应被拒
	req := httptest.NewRequest("GET", "/test", nil)
	req.RemoteAddr = "10.0.0.1:5555"
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusTooManyRequests {
		t.Errorf("over-limit: status = %d, want 429", rec.Code)
	}
	if rec.Header().Get("Retry-After") == "" {
		t.Error("429 response missing Retry-After header")
	}
}

// TestRateLimitMiddleware_IPIsolation 不同 IP 互不影响。
func TestRateLimitMiddleware_IPIsolation(t *testing.T) {
	lim := ratelimit.NewLimiter(1, 0, time.Minute) // 每 IP 只能 burst 1
	r := chi.NewRouter()
	r.Use(rateLimitMiddleware(lim, nil))
	r.Get("/test", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, nil)
	})

	// IP-A 用完
	reqA := httptest.NewRequest("GET", "/test", nil)
	reqA.RemoteAddr = "1.1.1.1:1111"
	r.ServeHTTP(httptest.NewRecorder(), reqA)

	// IP-A 应该被拒
	reqA2 := httptest.NewRequest("GET", "/test", nil)
	reqA2.RemoteAddr = "1.1.1.1:1111"
	recA2 := httptest.NewRecorder()
	r.ServeHTTP(recA2, reqA2)
	if recA2.Code != http.StatusTooManyRequests {
		t.Errorf("IP-A second: status = %d, want 429", recA2.Code)
	}

	// IP-B 应该通过
	reqB := httptest.NewRequest("GET", "/test", nil)
	reqB.RemoteAddr = "2.2.2.2:2222"
	recB := httptest.NewRecorder()
	r.ServeHTTP(recB, reqB)
	if recB.Code != http.StatusOK {
		t.Errorf("IP-B: status = %d, want 200", recB.Code)
	}
}

// TestRateLimitMiddleware_SkipPath 跳过路径不被限。
func TestRateLimitMiddleware_SkipPath(t *testing.T) {
	lim := ratelimit.NewLimiter(1, 0, time.Minute) // 只能 burst 1
	skipHealthz := func(path string) bool { return path == "/healthz" }
	r := chi.NewRouter()
	r.Use(rateLimitMiddleware(lim, skipHealthz))
	r.Get("/healthz", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	r.Get("/api/test", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, nil)
	})

	// 先用 /api/test 消耗 token
	req1 := httptest.NewRequest("GET", "/api/test", nil)
	req1.RemoteAddr = "10.0.0.1:1234"
	r.ServeHTTP(httptest.NewRecorder(), req1)

	// /api/test 应被拒
	req2 := httptest.NewRequest("GET", "/api/test", nil)
	req2.RemoteAddr = "10.0.0.1:1234"
	rec2 := httptest.NewRecorder()
	r.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusTooManyRequests {
		t.Errorf("api: status = %d, want 429", rec2.Code)
	}

	// /healthz 不受限
	req3 := httptest.NewRequest("GET", "/healthz", nil)
	req3.RemoteAddr = "10.0.0.1:1234"
	rec3 := httptest.NewRecorder()
	r.ServeHTTP(rec3, req3)
	if rec3.Code != http.StatusOK {
		t.Errorf("healthz: status = %d, want 200", rec3.Code)
	}
}

// TestRateLimitMiddleware_LoginEndpoint 模拟 login 端点的严格限流。
func TestRateLimitMiddleware_LoginEndpoint(t *testing.T) {
	loginLimiter := ratelimit.NewLimiter(3, 0, time.Minute) // 只能 burst 3
	r := chi.NewRouter()
	r.Route("/api/auth", func(r chi.Router) {
		r.With(rateLimitMiddleware(loginLimiter, nil)).Post("/login", func(w http.ResponseWriter, r *http.Request) {
			writeJSON(w, http.StatusOK, map[string]string{"logged_in": "true"})
		})
		r.Get("/me", func(w http.ResponseWriter, r *http.Request) {
			writeJSON(w, http.StatusOK, map[string]string{"me": "true"})
		})
	})

	// 3 次 login 应通过
	for i := 0; i < 3; i++ {
		req := httptest.NewRequest("POST", "/api/auth/login", nil)
		req.RemoteAddr = "192.168.1.1:9999"
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Errorf("login %d: status = %d, want 200", i, rec.Code)
		}
	}

	// 第 4 次被拒
	req := httptest.NewRequest("POST", "/api/auth/login", nil)
	req.RemoteAddr = "192.168.1.1:9999"
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusTooManyRequests {
		t.Errorf("login 4th: status = %d, want 429", rec.Code)
	}

	// /me 不受 login 限流影响
	reqMe := httptest.NewRequest("GET", "/api/auth/me", nil)
	reqMe.RemoteAddr = "192.168.1.1:9999"
	recMe := httptest.NewRecorder()
	r.ServeHTTP(recMe, reqMe)
	if recMe.Code != http.StatusOK {
		t.Errorf("me: status = %d, want 200", recMe.Code)
	}
}
