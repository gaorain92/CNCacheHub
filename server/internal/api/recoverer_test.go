package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestRecovererMiddleware_NoPanic 验证正常请求不被 recover 拦截。
func TestRecovererMiddleware_NoPanic(t *testing.T) {
	t.Parallel()
	mw := recovererMiddleware()

	calls := 0
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	h := mw(next)
	req := httptest.NewRequest("GET", "/test", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
	if calls != 1 {
		t.Fatalf("expected next to be called once, got %d", calls)
	}
	if rr.Body.String() != "ok" {
		t.Fatalf("expected body=ok, got %q", rr.Body.String())
	}
}

// TestRecovererMiddleware_Recovers 验证 panic 被捕获、500 返 + 写日志（这里不直接抓日志，
// 只验证响应符合预期）。
func TestRecovererMiddleware_Recovers(t *testing.T) {
	t.Parallel()
	mw := recovererMiddleware()

	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 抛 panic — 函数不会返回，recovererMiddleware 应该接住。
		panic("boom!")
	})

	h := mw(next)
	req := httptest.NewRequest("GET", "/test/panic", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", rr.Code)
	}
	// 响应 body 是 JSON 错误格式，不应暴露 panic 内容
	body := rr.Body.String()
	if !strings.Contains(body, `"code":"internal_error"`) {
		t.Fatalf("expected JSON error code=internal_error, got body=%q", body)
	}
	if strings.Contains(body, "boom") {
		t.Fatalf("panic value leaked to response body: %q", body)
	}
}

// TestRecovererMiddleware_ChainedNext 验证 recoverer + 后续 handler chain 都能正常处理。
// （覆盖中间件组合场景：recoverer 不应破坏正常的 handler 链路）
func TestRecovererMiddleware_ChainedNext(t *testing.T) {
	t.Parallel()
	mw := recovererMiddleware()

	calls := 0
	handler1 := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("X-H1", "ok")
		w.WriteHeader(http.StatusTeapot) // 418 — 模拟 handler 自己返的 status
	})

	// 链式：recoverer -> handler1（不 panic，验证 status code / headers 透传）
	chain := mw(handler1)
	req := httptest.NewRequest("GET", "/chained", nil)
	rr := httptest.NewRecorder()
	chain.ServeHTTP(rr, req)

	if rr.Code != http.StatusTeapot {
		t.Fatalf("expected 418 (Teapot), got %d", rr.Code)
	}
	if calls != 1 {
		t.Fatalf("expected handler1 called once, got %d", calls)
	}
	if rr.Header().Get("X-H1") != "ok" {
		t.Fatalf("handler1's headers should pass through, got %q", rr.Header().Get("X-H1"))
	}
}
