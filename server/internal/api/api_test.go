package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// fakePinger 是满足 Options.DB 接口的最小实现。
type fakePinger struct {
	err error
}

func (f *fakePinger) Ping(_ context.Context) error { return f.err }

// newTestHandler 构造一个测试用 handler。
func newTestHandler(db interface {
	Ping(ctx context.Context) error
}, startTime time.Time) http.Handler {
	return NewRouter(Options{
		DB:        db,
		StartTime: startTime,
		Build: BuildInfo{
			Name:    "cncachehub",
			Version: "test-version",
			Go:      "go1.22.5",
			Commit:  "abc1234",
		},
	})
}

// do 发起请求并返回 response。
func do(t *testing.T, h http.Handler, method, path string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

// TestHealthz_Root 验证 GET /healthz。
func TestHealthz_Root(t *testing.T) {
	h := newTestHandler(&fakePinger{}, time.Now())
	rr := do(t, h, http.MethodGet, "/healthz")

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	if ct := rr.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("Content-Type = %q, want application/json*", ct)
	}
	var body map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("json unmarshal: %v; body=%s", err, rr.Body.String())
	}
	if body["status"] != "ok" {
		t.Errorf("status = %v, want ok", body["status"])
	}
}

// TestHealthz_Api 验证 GET /api/healthz 包含 uptime / db / version。
func TestHealthz_Api(t *testing.T) {
	// 启动时间设为 5 秒前，确保 uptime >= "0s"。
	start := time.Now().Add(-5 * time.Second)
	h := newTestHandler(&fakePinger{}, start)
	rr := do(t, h, http.MethodGet, "/api/healthz")

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("json unmarshal: %v; body=%s", err, rr.Body.String())
	}
	if body["status"] != "ok" {
		t.Errorf("status = %v, want ok", body["status"])
	}
	uptime, ok := body["uptime"].(string)
	if !ok || uptime == "" {
		t.Errorf("uptime = %v (type %T), want non-empty string", body["uptime"], body["uptime"])
	}
	if body["db"] != "ok" {
		t.Errorf("db = %v, want ok", body["db"])
	}
	if body["version"] != "test-version" {
		t.Errorf("version = %v, want test-version", body["version"])
	}
}

// TestHealthz_Api_DBError 验证 db ping 失败时 db 字段反映错误。
func TestHealthz_Api_DBError(t *testing.T) {
	h := newTestHandler(&fakePinger{err: context.DeadlineExceeded}, time.Now())
	rr := do(t, h, http.MethodGet, "/api/healthz")

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (db 错误不应让 /api/healthz 失败)", rr.Code)
	}
	var body map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("json unmarshal: %v", err)
	}
	dbVal, _ := body["db"].(string)
	if !strings.HasPrefix(dbVal, "error:") {
		t.Errorf("db = %q, want prefix 'error:'", dbVal)
	}
}

// TestVersion 验证 /api/version 输出。
func TestVersion(t *testing.T) {
	h := newTestHandler(&fakePinger{}, time.Now())
	rr := do(t, h, http.MethodGet, "/api/version")

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	var body map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("json unmarshal: %v; body=%s", err, rr.Body.String())
	}
	for _, k := range []string{"name", "version", "go", "commit"} {
		if _, ok := body[k].(string); !ok {
			t.Errorf("%s missing or not a string: %v", k, body[k])
		}
	}
	if body["name"] != "cncachehub" {
		t.Errorf("name = %v, want cncachehub", body["name"])
	}
	if body["version"] != "test-version" {
		t.Errorf("version = %v, want test-version", body["version"])
	}
	if body["commit"] != "abc1234" {
		t.Errorf("commit = %v, want abc1234", body["commit"])
	}
}

// TestVersion_Defaults 验证 BuildInfo 为零值时仍输出有效默认值。
func TestVersion_Defaults(t *testing.T) {
	h := NewRouter(Options{
		DB:        &fakePinger{},
		StartTime: time.Now(),
		Build:     BuildInfo{}, // 全部零值
	})
	rr := do(t, h, http.MethodGet, "/api/version")
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	var body map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("json unmarshal: %v", err)
	}
	if body["name"] != "cncachehub" {
		t.Errorf("name default = %v, want cncachehub", body["name"])
	}
	if body["version"] != "dev" {
		t.Errorf("version default = %v, want dev", body["version"])
	}
	if body["commit"] != "local" {
		t.Errorf("commit default = %v, want local", body["commit"])
	}
	if body["go"] == "" {
		t.Errorf("go default = empty, want runtime.Version()")
	}
}

// TestNotFound 验证 404 返回统一错误格式。
func TestNotFound(t *testing.T) {
	h := newTestHandler(&fakePinger{}, time.Now())
	rr := do(t, h, http.MethodGet, "/no-such-route")
	if rr.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rr.Code)
	}
	var body map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("json unmarshal: %v", err)
	}
	errObj, ok := body["error"].(map[string]any)
	if !ok {
		t.Fatalf("error object missing: %v", body)
	}
	if errObj["code"] != "not_found" {
		t.Errorf("code = %v, want not_found", errObj["code"])
	}
	if msg, _ := errObj["message"].(string); !strings.Contains(msg, "/no-such-route") {
		t.Errorf("message = %q, want contains /no-such-route", msg)
	}
}

// TestMethodNotAllowed 验证 405 返回统一错误格式。
func TestMethodNotAllowed(t *testing.T) {
	h := newTestHandler(&fakePinger{}, time.Now())
	rr := do(t, h, http.MethodPost, "/healthz")
	if rr.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", rr.Code)
	}
	var body map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("json unmarshal: %v", err)
	}
	errObj, ok := body["error"].(map[string]any)
	if !ok {
		t.Fatalf("error object missing: %v", body)
	}
	if errObj["code"] != "method_not_allowed" {
		t.Errorf("code = %v, want method_not_allowed", errObj["code"])
	}
}

// TestRequestID 验证 X-Request-Id 出现在响应头。
func TestRequestID(t *testing.T) {
	h := newTestHandler(&fakePinger{}, time.Now())
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	req.Header.Set("X-Request-Id", "test-rid-42")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if got := rr.Header().Get("X-Request-Id"); got != "test-rid-42" {
		t.Errorf("X-Request-Id = %q, want test-rid-42", got)
	}
}

// TestHealthz_Api_NoDB 验证 DB 为 nil 时 db 字段为 "not_configured"。
func TestHealthz_Api_NoDB(t *testing.T) {
	h := NewRouter(Options{
		DB:        nil,
		StartTime: time.Now(),
		Build:     BuildInfo{Name: "cncachehub", Version: "v"},
	})
	rr := do(t, h, http.MethodGet, "/api/healthz")
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	var body map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("json unmarshal: %v", err)
	}
	if body["db"] != "not_configured" {
		t.Errorf("db = %v, want not_configured", body["db"])
	}
}

// TestAccessLogRecord_JSON_CamelCase 回归测试：AccessLogRecord 的 JSON 字段必须
// 是 camelCase（前端用 method/path/status/...），绝不能是 PascalCase（Go 默认）。
//
// 历史 bug：之前 AccessLogRecord 没 json tag，handler 序列化出 "Method"/"Path"/"Status"
// 大写字段，前端读 log.method 拿不到值，UI 看起来"没东西"。
func TestAccessLogRecord_JSON_CamelCase(t *testing.T) {
	rec := AccessLogRecord{
		Method:       "GET",
		Path:         "/v2/library/nginx/blobs/sha256:abc",
		Status:       200,
		DurationMs:   123,
		Cached:       true,
		Bypassed:     false,
		BypassReason: "size_limit",
		ClientIP:     "1.2.3.4",
		Bytes:        1024,
		Error:        "",
	}
	b, err := json.Marshal(rec)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	var body map[string]any
	if err := json.Unmarshal(b, &body); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}

	// 必须是小写 camelCase
	for _, k := range []string{"method", "path", "status", "durationMs", "cached", "bypassed", "bypassReason", "clientIp", "bytes", "error"} {
		if _, ok := body[k]; !ok {
			t.Errorf("expected camelCase key %q, got body=%s", k, string(b))
		}
	}
	// 不能是 PascalCase（确认 PascalCase key 不存在）
	for _, k := range []string{"Method", "Path", "Status", "DurationMs", "Cached", "Bypassed", "BypassReason", "ClientIP", "Bytes", "Error"} {
		if _, ok := body[k]; ok {
			t.Errorf("unexpected PascalCase key %q, body=%s", k, string(b))
		}
	}
	// 验证值正确
	if body["method"] != "GET" {
		t.Errorf("method = %v, want GET", body["method"])
	}
	if body["status"] != float64(200) {
		t.Errorf("status = %v, want 200", body["status"])
	}
	if body["bypassReason"] != "size_limit" {
		t.Errorf("bypassReason = %v, want size_limit", body["bypassReason"])
	}
}
