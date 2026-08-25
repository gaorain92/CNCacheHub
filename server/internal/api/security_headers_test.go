// Tests for security headers in middleware.
package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestJSONContentTypeMiddleware_SetsSecurityHeaders 验证安全头被设置。
func TestJSONContentTypeMiddleware_SetsSecurityHeaders(t *testing.T) {
	mw := jsonContentTypeMiddleware()
	h := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/test", nil)
	h.ServeHTTP(w, r)

	if got := w.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Errorf("X-Content-Type-Options = %q, want nosniff", got)
	}
	if got := w.Header().Get("X-Frame-Options"); got != "DENY" {
		t.Errorf("X-Frame-Options = %q, want DENY", got)
	}
	if got := w.Header().Get("Referrer-Policy"); got != "no-referrer" {
		t.Errorf("Referrer-Policy = %q, want no-referrer", got)
	}
	if got := w.Header().Get("Content-Type"); got != "application/json; charset=utf-8" {
		t.Errorf("Content-Type = %q, want application/json; charset=utf-8", got)
	}
}

// TestJSONContentTypeMiddleware_PreservesExplicitContentType
// 验证 handler 显式设的 Content-Type 不被覆盖（diagnostics bundle 等场景）。
func TestJSONContentTypeMiddleware_PreservesExplicitContentType(t *testing.T) {
	mw := jsonContentTypeMiddleware()
	h := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/gzip")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("fake-gzip-data"))
	}))
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/diagnostics/bundle", nil)
	h.ServeHTTP(w, r)

	if got := w.Header().Get("Content-Type"); got != "application/gzip" {
		t.Errorf("Content-Type = %q, want application/gzip (handler explicit)", got)
	}
}
