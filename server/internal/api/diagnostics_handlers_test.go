package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestBundleHandler_RequiresAdmin 验证非 admin 调用被 403 拦截。
func TestBundleHandler_RequiresAdmin(t *testing.T) {
	opts := Options{
		SessionUserRole: func(ctx context.Context, r *http.Request) (string, int64, error) {
			return "user", 1, nil
		},
		// BundleSource 留空，DB=nil
	}
	r := newRouterForTest(opts)
	req := httptest.NewRequest("POST", "/api/diagnostics/bundle", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Errorf("code = %d, want 403", rr.Code)
	}
}

// TestBundleHandler_NoDB503 验证 admin 但 DB 没注入时返回 503。
func TestBundleHandler_NoDB503(t *testing.T) {
	opts := Options{
		SessionUserRole: func(ctx context.Context, r *http.Request) (string, int64, error) {
			return "admin", 1, nil
		},
		// BundleSource 缺省（DB=nil）
	}
	r := newRouterForTest(opts)
	req := httptest.NewRequest("POST", "/api/diagnostics/bundle", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusServiceUnavailable {
		t.Errorf("code = %d, want 503", rr.Code)
	}
}

// newRouterForTest 构造一个最小 chi router 装上 bundle handler + SessionUserRole。
//
// 走 requireAuth 链需要 AuthDB 字段，我们绕开：直接在 r 上挂 handler，绕过全局 middleware。
func newRouterForTest(opts Options) http.Handler {
	// 直接挂 handler 跳进诊断路由块（admin-only 中间件内嵌）
	mux := http.NewServeMux()
	mux.HandleFunc("/api/diagnostics/bundle", diagnosticsBundleHandler(opts))
	return mux
}
