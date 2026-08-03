package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	chimw "github.com/go-chi/chi/v5/middleware"
)

// ---------------------------------------------------------------------------
// writeJSON — 边界
// ---------------------------------------------------------------------------

func TestWriteJSON_Struct(t *testing.T) {
	rr := httptest.NewRecorder()
	writeJSON(rr, http.StatusOK, struct {
		Name string `json:"name"`
	}{Name: "alice"})

	if rr.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rr.Code)
	}
	if ct := rr.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}
	var got map[string]string
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if got["name"] != "alice" {
		t.Errorf("body.name = %q, want alice", got["name"])
	}
}

func TestWriteJSON_Slice(t *testing.T) {
	rr := httptest.NewRecorder()
	writeJSON(rr, http.StatusOK, []string{"a", "b", "c"})
	var got []string
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if len(got) != 3 || got[0] != "a" {
		t.Errorf("body = %v, want [a b c]", got)
	}
}

func TestWriteJSON_NilValue(t *testing.T) {
	rr := httptest.NewRecorder()
	writeJSON(rr, http.StatusOK, nil)
	// nil 序列化成 "null\n"（json.Encoder 默认带换行）
	body := strings.TrimSpace(rr.Body.String())
	if body != "null" {
		t.Errorf("body = %q, want 'null'", body)
	}
}

func TestWriteJSON_CustomStatus(t *testing.T) {
	rr := httptest.NewRecorder()
	writeJSON(rr, http.StatusTeapot, map[string]string{"teapot": "true"})
	if rr.Code != http.StatusTeapot {
		t.Errorf("status = %d, want 418", rr.Code)
	}
}

func TestWriteJSON_UnencodableValue(t *testing.T) {
	// channel / func 是 JSON unencodable
	rr := httptest.NewRecorder()
	writeJSON(rr, http.StatusOK, make(chan int))
	// 现状：Encode 会失败但不写 status（header 已写）— 记录行为不 panic
	// 不做严格断言，只确保不 panic
}

// ---------------------------------------------------------------------------
// httpError
// ---------------------------------------------------------------------------

func TestHTTPError_Error(t *testing.T) {
	e := &httpError{Status: 400, Code: "bad_request", Message: "invalid input"}
	if got := e.Error(); got != "invalid input" {
		t.Errorf("Error() = %q, want 'invalid input'", got)
	}
}

func TestNewHTTPError(t *testing.T) {
	e := newHTTPError(http.StatusNotFound, "not_found", "user not found")
	if e.Status != http.StatusNotFound {
		t.Errorf("Status = %d, want 404", e.Status)
	}
	if e.Code != "not_found" {
		t.Errorf("Code = %q, want not_found", e.Code)
	}
	if e.Message != "user not found" {
		t.Errorf("Message = %q, want 'user not found'", e.Message)
	}
}

func TestAsHTTPError_HttpError(t *testing.T) {
	original := newHTTPError(http.StatusForbidden, "forbidden", "no access")
	var err error = original
	got, ok := asHTTPError(err)
	if !ok {
		t.Fatal("asHTTPError should succeed for *httpError")
	}
	if got != original {
		t.Error("returned different pointer")
	}
}

func TestAsHTTPError_WrappedHttpError(t *testing.T) {
	// fmt.Errorf("...: %w", httpErr) 包装
	original := newHTTPError(http.StatusBadRequest, "bad", "x")
	wrapped := errors.Join(errors.New("outer"), original)
	got, ok := asHTTPError(wrapped)
	if !ok {
		t.Fatal("asHTTPError should unwrap *httpError")
	}
	if got.Code != "bad" {
		t.Errorf("Code = %q, want bad", got.Code)
	}
}

func TestAsHTTPError_OtherError(t *testing.T) {
	plain := errors.New("plain error")
	got, ok := asHTTPError(plain)
	if ok {
		t.Error("asHTTPError should return false for non-httpError")
	}
	if got != nil {
		t.Errorf("got = %v, want nil", got)
	}
}

func TestAsHTTPError_Nil(t *testing.T) {
	got, ok := asHTTPError(nil)
	if ok || got != nil {
		t.Errorf("asHTTPError(nil) = (%v, %v), want (nil, false)", got, ok)
	}
}

// ---------------------------------------------------------------------------
// generateRequestID — 唯一性
// ---------------------------------------------------------------------------

func TestGenerateRequestID_Unique(t *testing.T) {
	seen := make(map[string]bool, 1000)
	for i := 0; i < 1000; i++ {
		id := generateRequestID()
		if id == "" {
			t.Fatal("generateRequestID returned empty")
		}
		if seen[id] {
			t.Fatalf("collision at iteration %d: %s", i, id)
		}
		seen[id] = true
	}
}

func TestGenerateRequestID_Format(t *testing.T) {
	id := generateRequestID()
	// 形式: <host>/<base64>-<6-digit-counter> — 不验具体内容，只验非空 + 长度合理
	if len(id) < 8 {
		t.Errorf("id too short: %q", id)
	}
	// 至少包含 "/" 分隔符
	if !strings.Contains(id, "/") {
		t.Errorf("id should contain '/', got %q", id)
	}
}

// ---------------------------------------------------------------------------
// requestIDMiddleware — 写入 ctx + header
// ---------------------------------------------------------------------------

func TestRequestIDMiddleware_NewID(t *testing.T) {
	req := httptest.NewRequest("GET", "/", nil)
	rr := httptest.NewRecorder()
	var capturedID string
	handler := requestIDMiddleware()
	final := handler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedID = chimw.GetReqID(r.Context())
		w.WriteHeader(200)
	}))
	final.ServeHTTP(rr, req)
	if capturedID == "" {
		t.Error("request id should be set in ctx")
	}
	if got := rr.Header().Get("X-Request-Id"); got != capturedID {
		t.Errorf("X-Request-Id header = %q, want %q", got, capturedID)
	}
}

func TestRequestIDMiddleware_ReuseClientID(t *testing.T) {
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("X-Request-Id", "client-supplied-id-123")
	rr := httptest.NewRecorder()
	var capturedID string
	handler := requestIDMiddleware()
	final := handler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedID = chimw.GetReqID(r.Context())
		w.WriteHeader(200)
	}))
	final.ServeHTTP(rr, req)
	if capturedID != "client-supplied-id-123" {
		t.Errorf("ctx id = %q, want client-supplied-id-123", capturedID)
	}
	if got := rr.Header().Get("X-Request-Id"); got != "client-supplied-id-123" {
		t.Errorf("X-Request-Id = %q, want client-supplied-id-123", got)
	}
}

// ---------------------------------------------------------------------------
// jsonContentTypeMiddleware
// ---------------------------------------------------------------------------

func TestJSONContentTypeMiddleware(t *testing.T) {
	req := httptest.NewRequest("GET", "/api/whatever", nil)
	rr := httptest.NewRecorder()
	handler := jsonContentTypeMiddleware()
	called := false
	final := handler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(200)
	}))
	final.ServeHTTP(rr, req)
	if !called {
		t.Error("next handler not called")
	}
	// 现状：jsonContentTypeMiddleware 不主动设 Content-Type（writeJSON 会设）
	// 这里只确保不 panic + next 被调用
}

// ---------------------------------------------------------------------------
// loggerMiddleware — smoke
// ---------------------------------------------------------------------------

func TestLoggerMiddleware_Logs(t *testing.T) {
	// loggerMiddleware 写 stdout/stderr — smoke test 只确保不 panic
	req := httptest.NewRequest("GET", "/", nil)
	rr := httptest.NewRecorder()
	handler := loggerMiddleware()
	final := handler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		_, _ = w.Write([]byte("ok"))
	}))
	final.ServeHTTP(rr, req)
	if rr.Code != 200 {
		t.Errorf("status = %d, want 200", rr.Code)
	}
}

// ---------------------------------------------------------------------------
// recovererMiddleware — smoke（已 panic 时返 500）
// 注：详细测试在 recoverer_test.go
// ---------------------------------------------------------------------------
