package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/cncachehub/server/internal/storage"
	"github.com/go-chi/chi/v5"
)

// ----- compileHFPattern 单元测试（纯函数） -----

func TestCompileHFPattern(t *testing.T) {
	cases := []struct {
		pat  string
		path string
		want bool
	}{
		{"*", "anywhere/file.bin", true},
		{"*", "a/b/c/d.bin", true},
		{"*.safetensors", "model.safetensors", true},
		{"*.safetensors", "sub/model.safetensors", true},
		{"*.safetensors", "model.bin", false},
		{"config.*", "config.json", true},
		{"config.*", "config.yaml", true},
		{"config.*", "tokenizer_config.json", false},
		{"tokenizer*", "tokenizer.json", true},
		{"tokenizer*", "tokenizer_config.json", true},
		{"tokenizer*", "model.safetensors", false},
		{"model*.bin", "model-00001-of-00003.bin", true},
		{"exact.json", "exact.json", true},
		{"exact.json", "sub/exact.json", true},
		{"exact.json", "exact.yaml", false},
		{"onnx/", "onnx/model.onnx", true},
		{"onnx/", "model.onnx", false},
		// path-prefix: "subdir/" 也匹配子文件
		{"subdir/", "subdir/file", true},
		{"subdir/", "subdir/file.bin", true},
		{"subdir/", "model.onnx", false},
		{"foo*bar*baz", "foo123bar456baz", true},
		{"foo*bar*baz", "foo123bar", false},
	}
	for _, c := range cases {
		t.Run(c.pat+"_vs_"+c.path, func(t *testing.T) {
			fn, err := compileHFPattern(c.pat)
			if err != nil {
				t.Fatalf("compileHFPattern(%q) err: %v", c.pat, err)
			}
			got := fn(c.path)
			if got != c.want {
				t.Errorf("match(%q, %q) = %v, want %v", c.pat, c.path, got, c.want)
			}
		})
	}
}

func TestCompileHFPattern_EmptyRejected(t *testing.T) {
	if _, err := compileHFPattern("   "); err == nil {
		t.Errorf("empty pattern should error")
	}
	if _, err := compileHFPattern(""); err == nil {
		t.Errorf("empty pattern should error")
	}
}

// ----- escapeHFPathSegment 单元测试 -----

func TestEscapeHFPathSegment_PreservesSlash(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"Qwen/Qwen2.5-1.5B-Instruct", "Qwen/Qwen2.5-1.5B-Instruct"},
		{"main", "main"},
		{"v1.0.0", "v1.0.0"},
		{"foo bar", "foo%20bar"},
		{"foo?bar", "foo%3Fbar"},
		{"a&b", "a%26b"},
		{"100%", "100%25"},
		// 双重 slash 也保留（HF 不允许，但 path 层面不拦）
		{"a/b/c", "a/b/c"},
	}
	for _, c := range cases {
		t.Run(c.in, func(t *testing.T) {
			got := escapeHFPathSegment(c.in)
			if got != c.want {
				t.Errorf("escapeHFPathSegment(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

// ----- handler 测试基础设施 -----

func hfTestOpts(t *testing.T, db *storage.DB, list func(ctx context.Context, modelID, revision string) ([]huggingFaceFile, error)) Options {
	t.Helper()
	return Options{
		CreatePreheatTask:    db.CreatePreheatTask,
		SessionUserRole:      func(ctx context.Context, r *http.Request) (string, int64, error) { return "admin", 1, nil },
		FetchHuggingFaceTree: list,
		GetHuggingFaceToken:  func() string { return "" },
	}
}

func TestHuggingFaceTree_Ok(t *testing.T) {
	dir := t.TempDir()
	db, _ := storage.Open(context.Background(), dir+"/test.db")
	t.Cleanup(func() { _ = db.Close() })

	want := []huggingFaceFile{
		{Type: "file", Path: "config.json", Size: 100},
		{Type: "file", Path: "model.safetensors", Size: 5_000_000_000},
		{Type: "directory", Path: "onnx"}, // 应被过滤
	}
	list := func(ctx context.Context, modelID, revision string) ([]huggingFaceFile, error) {
		if modelID != "Qwen/Qwen2.5-1.5B-Instruct" || revision != "main" {
			t.Errorf("unexpected tree query: %s @ %s", modelID, revision)
		}
		return want, nil
	}
	r := chi.NewRouter()
	r.Get("/api/huggingface/models/*", huggingFaceTreeHandler(hfTestOpts(t, db, list)))

	req := httptest.NewRequest("GET", "/api/huggingface/models/Qwen/Qwen2.5-1.5B-Instruct/tree", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("code=%d, body=%s", rr.Code, rr.Body.String())
	}
	var resp huggingFaceTreeResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v, body=%s", err, rr.Body.String())
	}
	if resp.ModelID != "Qwen/Qwen2.5-1.5B-Instruct" {
		t.Errorf("modelId = %q", resp.ModelID)
	}
	if resp.Total != 2 {
		t.Errorf("Total = %d, want 2 (directory filtered out)", resp.Total)
	}
	if resp.Files[0].Path != "config.json" || resp.Files[1].Path != "model.safetensors" {
		t.Errorf("files = %+v", resp.Files)
	}
}

func TestHuggingFaceTree_TreeError(t *testing.T) {
	dir := t.TempDir()
	db, _ := storage.Open(context.Background(), dir+"/test.db")
	t.Cleanup(func() { _ = db.Close() })

	list := func(ctx context.Context, modelID, revision string) ([]huggingFaceFile, error) {
		// 模拟 HF 401 + body — 透传消息
		return nil, &hfAuthError{Status: 401, Body: "rate limited, retry in 30s"}
	}
	r := chi.NewRouter()
	r.Get("/api/huggingface/models/*", huggingFaceTreeHandler(hfTestOpts(t, db, list)))

	req := httptest.NewRequest("GET", "/api/huggingface/models/gated/repo/tree", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("code=%d, body=%s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "hf_token_required") {
		t.Errorf("body = %s, want hf_token_required", rr.Body.String())
	}
	// 关键：HF 真消息（rate limited）应该被透传出来
	if !strings.Contains(rr.Body.String(), "rate limited") {
		t.Errorf("body should contain HF actual message, got: %s", rr.Body.String())
	}
}

func TestHuggingFaceTree_TreeError_WithTokenConfigured(t *testing.T) {
	dir := t.TempDir()
	db, _ := storage.Open(context.Background(), dir+"/test.db")
	t.Cleanup(func() { _ = db.Close() })

	list := func(ctx context.Context, modelID, revision string) ([]huggingFaceFile, error) {
		return nil, &hfAuthError{Status: 403, Body: "gated repo"}
	}
	opts := hfTestOpts(t, db, list)
	// 模拟用户配了 token 但仍 403 — 确认是 gated
	opts.GetHuggingFaceToken = func() string { return "hf_user_configured_token" }

	r := chi.NewRouter()
	r.Get("/api/huggingface/models/*", huggingFaceTreeHandler(opts))
	req := httptest.NewRequest("GET", "/api/huggingface/models/some/private/repo/tree", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Fatalf("code=%d", rr.Code)
	}
	// 有 token 时提示语应该不同：明确说"token 配了但仍 403"
	if !strings.Contains(rr.Body.String(), "even with token configured") {
		t.Errorf("body should mention token configured, got: %s", rr.Body.String())
	}
}

func TestHuggingFaceTree_TreeError_NoToken_ListsPossibleCauses(t *testing.T) {
	dir := t.TempDir()
	db, _ := storage.Open(context.Background(), dir+"/test.db")
	t.Cleanup(func() { _ = db.Close() })

	list := func(ctx context.Context, modelID, revision string) ([]huggingFaceFile, error) {
		return nil, &hfAuthError{Status: 429, Body: "rate limited"}
	}
	opts := hfTestOpts(t, db, list)
	// 无 token + 429 — 提示要看 3 种可能
	opts.GetHuggingFaceToken = func() string { return "" }

	r := chi.NewRouter()
	r.Get("/api/huggingface/models/*", huggingFaceTreeHandler(opts))
	req := httptest.NewRequest("GET", "/api/huggingface/models/some/repo/tree", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Fatalf("code=%d", rr.Code)
	}
	// 应该列举可能原因（gated / rate limit / HF down）
	if !strings.Contains(rr.Body.String(), "rate limited") {
		t.Errorf("body should include HF message, got: %s", rr.Body.String())
	}
}

func TestHFAuthError_Is(t *testing.T) {
	e := &hfAuthError{Status: 401, Body: "x"}
	// errors.Is(e, errHFAuthRequired) 应该为 true（通过 Is 方法）
	if !errors.Is(e, errHFAuthRequired) {
		t.Error("hfAuthError should match errHFAuthRequired via Is()")
	}
	// 直接 ==
	if e == errHFAuthRequired {
		t.Error("hfAuthError pointer should not equal sentinel")
	}
	// errors.As 应该能拿到 hfAuthError
	var got *hfAuthError
	if !errors.As(e, &got) {
		t.Error("errors.As should extract hfAuthError")
	}
	if got.Status != 401 || got.Body != "x" {
		t.Errorf("As: got %+v", got)
	}
}

func TestHuggingFaceTree_WhitespaceModelID(t *testing.T) {
	dir := t.TempDir()
	db, _ := storage.Open(context.Background(), dir+"/test.db")
	t.Cleanup(func() { _ = db.Close() })

	r := chi.NewRouter()
	r.Get("/api/huggingface/models/*", huggingFaceTreeHandler(hfTestOpts(t, db, nil)))
	// %20 = 空格；handler 应拦下
	req := httptest.NewRequest("GET", "/api/huggingface/models/"+url.PathEscape("foo bar")+"/tree", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("code = %d, want 400 (whitespace in modelId)", rr.Code)
	}
}

// ----- preheat handler 测试 -----

func TestHuggingFacePreheat_CreatesTask(t *testing.T) {
	dir := t.TempDir()
	db, _ := storage.Open(context.Background(), dir+"/test.db")
	t.Cleanup(func() { _ = db.Close() })

	list := func(ctx context.Context, modelID, revision string) ([]huggingFaceFile, error) {
		return []huggingFaceFile{
			{Type: "file", Path: "config.json", Size: 100},
			{Type: "file", Path: "model.safetensors", Size: 5_000_000_000},
			{Type: "file", Path: "onnx/model.onnx", Size: 1_000_000},
			{Type: "file", Path: "tokenizer.json", Size: 5000},
		}, nil
	}
	r := chi.NewRouter()
	r.Post("/api/huggingface/preheat", huggingFacePreheatHandler(hfTestOpts(t, db, list)))

	body := bytes.NewBufferString(`{"modelId":"Qwen/Qwen2.5-1.5B-Instruct","revision":"main","patterns":["*.json","*.safetensors"]}`)
	req := httptest.NewRequest("POST", "/api/huggingface/preheat", body)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusCreated {
		t.Fatalf("code=%d, body=%s", rr.Code, rr.Body.String())
	}
	var resp huggingFacePreheatResponse
	_ = json.Unmarshal(rr.Body.Bytes(), &resp)
	if resp.Filtered != 3 {
		t.Errorf("Filtered = %d, want 3 (config.json + model.safetensors + tokenizer.json)", resp.Filtered)
	}
	if resp.BytesTotal != 5_000_005_100 {
		t.Errorf("BytesTotal = %d", resp.BytesTotal)
	}
	if resp.Task.Kind != storage.PreheatKindHuggingFaceModel {
		t.Errorf("Kind = %q", resp.Task.Kind)
	}
	if len(resp.Task.Targets) != 3 {
		t.Errorf("Targets len = %d", len(resp.Task.Targets))
	}
	if !strings.HasPrefix(resp.Task.Targets[0], "Qwen/Qwen2.5-1.5B-Instruct|main|") {
		t.Errorf("Target[0] = %q", resp.Task.Targets[0])
	}
}

func TestHuggingFacePreheat_AllWhenNoPatterns(t *testing.T) {
	dir := t.TempDir()
	db, _ := storage.Open(context.Background(), dir+"/test.db")
	t.Cleanup(func() { _ = db.Close() })

	list := func(ctx context.Context, modelID, revision string) ([]huggingFaceFile, error) {
		return []huggingFaceFile{
			{Type: "file", Path: "config.json", Size: 100},
			{Type: "file", Path: "model.bin", Size: 1000},
		}, nil
	}
	r := chi.NewRouter()
	r.Post("/api/huggingface/preheat", huggingFacePreheatHandler(hfTestOpts(t, db, list)))

	req := httptest.NewRequest("POST", "/api/huggingface/preheat",
		bytes.NewBufferString(`{"modelId":"foo/bar"}`))
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusCreated {
		t.Fatalf("code=%d body=%s", rr.Code, rr.Body.String())
	}
	var resp huggingFacePreheatResponse
	_ = json.Unmarshal(rr.Body.Bytes(), &resp)
	if resp.Filtered != 2 {
		t.Errorf("Filtered = %d, want 2 (no patterns = all)", resp.Filtered)
	}
}

func TestHuggingFacePreheat_Truncates(t *testing.T) {
	dir := t.TempDir()
	db, _ := storage.Open(context.Background(), dir+"/test.db")
	t.Cleanup(func() { _ = db.Close() })

	files := make([]huggingFaceFile, 50)
	for i := range files {
		files[i] = huggingFaceFile{Type: "file", Path: "f" + string(rune('0'+i%10)) + ".bin", Size: 100}
	}
	list := func(ctx context.Context, modelID, revision string) ([]huggingFaceFile, error) {
		return files, nil
	}
	r := chi.NewRouter()
	r.Post("/api/huggingface/preheat", huggingFacePreheatHandler(hfTestOpts(t, db, list)))

	req := httptest.NewRequest("POST", "/api/huggingface/preheat",
		bytes.NewBufferString(`{"modelId":"foo/bar","maxFiles":10}`))
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusCreated {
		t.Fatalf("code=%d body=%s", rr.Code, rr.Body.String())
	}
	var resp huggingFacePreheatResponse
	_ = json.Unmarshal(rr.Body.Bytes(), &resp)
	if !resp.Truncated {
		t.Errorf("Truncated = false, want true")
	}
	if resp.Filtered != 10 {
		t.Errorf("Filtered = %d, want 10", resp.Filtered)
	}
}

func TestHuggingFacePreheat_NoMatch(t *testing.T) {
	dir := t.TempDir()
	db, _ := storage.Open(context.Background(), dir+"/test.db")
	t.Cleanup(func() { _ = db.Close() })

	list := func(ctx context.Context, modelID, revision string) ([]huggingFaceFile, error) {
		return []huggingFaceFile{
			{Type: "file", Path: "model.safetensors", Size: 1000},
		}, nil
	}
	r := chi.NewRouter()
	r.Post("/api/huggingface/preheat", huggingFacePreheatHandler(hfTestOpts(t, db, list)))

	req := httptest.NewRequest("POST", "/api/huggingface/preheat",
		bytes.NewBufferString(`{"modelId":"foo/bar","patterns":["*.json"]}`))
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("code=%d body=%s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "no_files_matched") {
		t.Errorf("body = %s", rr.Body.String())
	}
}

func TestHuggingFacePreheat_RequiresAdmin(t *testing.T) {
	dir := t.TempDir()
	db, _ := storage.Open(context.Background(), dir+"/test.db")
	t.Cleanup(func() { _ = db.Close() })

	opts := hfTestOpts(t, db, nil)
	opts.SessionUserRole = func(ctx context.Context, r *http.Request) (string, int64, error) {
		return "user", 2, nil
	}
	r := chi.NewRouter()
	r.Post("/api/huggingface/preheat", huggingFacePreheatHandler(opts))

	req := httptest.NewRequest("POST", "/api/huggingface/preheat",
		bytes.NewBufferString(`{"modelId":"foo/bar"}`))
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Errorf("code = %d, want 403", rr.Code)
	}
}

func TestHuggingFacePreheat_MissingModelID(t *testing.T) {
	dir := t.TempDir()
	db, _ := storage.Open(context.Background(), dir+"/test.db")
	t.Cleanup(func() { _ = db.Close() })

	r := chi.NewRouter()
	r.Post("/api/huggingface/preheat", huggingFacePreheatHandler(hfTestOpts(t, db, nil)))

	req := httptest.NewRequest("POST", "/api/huggingface/preheat",
		bytes.NewBufferString(`{}`))
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("code = %d, want 400", rr.Code)
	}
}

func TestHuggingFacePreheat_TokenError(t *testing.T) {
	dir := t.TempDir()
	db, _ := storage.Open(context.Background(), dir+"/test.db")
	t.Cleanup(func() { _ = db.Close() })

	list := func(ctx context.Context, modelID, revision string) ([]huggingFaceFile, error) {
		return nil, errHFAuthRequired
	}
	r := chi.NewRouter()
	r.Post("/api/huggingface/preheat", huggingFacePreheatHandler(hfTestOpts(t, db, list)))

	req := httptest.NewRequest("POST", "/api/huggingface/preheat",
		bytes.NewBufferString(`{"modelId":"gated/repo"}`))
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	// 用 403 而非 401：避免前端全局拦截器误判为 session 失效
	if rr.Code != http.StatusForbidden {
		t.Errorf("code = %d, want 403", rr.Code)
	}
}

// ===== cache cap 防护 =====

func TestHuggingFacePreheat_RespectsCacheCap(t *testing.T) {
	dir := t.TempDir()
	db, _ := storage.Open(context.Background(), dir+"/test.db")
	t.Cleanup(func() { _ = db.Close() })

	// 100 个 1GB 文件 = 100GB，远超 20GB cap
	big := make([]huggingFaceFile, 100)
	for i := range big {
		big[i] = huggingFaceFile{Type: "file", Path: "f.bin", Size: 1 << 30}
	}
	list := func(ctx context.Context, modelID, revision string) ([]huggingFaceFile, error) {
		return big, nil
	}
	opts := hfTestOpts(t, db, list)
	opts.GetSettings = func(ctx context.Context) (SystemSettings, error) {
		return SystemSettings{CacheTotalGB: 20}, nil
	}
	r := chi.NewRouter()
	r.Post("/api/huggingface/preheat", huggingFacePreheatHandler(opts))

	req := httptest.NewRequest("POST", "/api/huggingface/preheat",
		bytes.NewBufferString(`{"modelId":"big/model"}`))
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	// 拒绝时返 200 + refused=true（不是 4xx — 是业务拒绝，不是协议错误）
	if rr.Code != http.StatusOK {
		t.Fatalf("code=%d, body=%s", rr.Code, rr.Body.String())
	}
	var resp huggingFacePreheatResponse
	_ = json.Unmarshal(rr.Body.Bytes(), &resp)
	if !resp.Refused {
		t.Errorf("Refused = false, want true (100GB > 20GB cap)")
	}
	if resp.CacheCapGB != 20 {
		t.Errorf("CacheCapGB = %d, want 20", resp.CacheCapGB)
	}
	if resp.Task.ID != 0 {
		t.Errorf("Task.ID = %d, want 0 (no task should be created)", resp.Task.ID)
	}
	if !strings.Contains(resp.RefusedWhy, "20") {
		t.Errorf("RefusedWhy should mention cap, got: %q", resp.RefusedWhy)
	}
}

func TestHuggingFacePreheat_ForceBypassesCacheCap(t *testing.T) {
	dir := t.TempDir()
	db, _ := storage.Open(context.Background(), dir+"/test.db")
	t.Cleanup(func() { _ = db.Close() })

	big := []huggingFaceFile{{Type: "file", Path: "f.bin", Size: 50 << 30}} // 50GB
	list := func(ctx context.Context, modelID, revision string) ([]huggingFaceFile, error) {
		return big, nil
	}
	opts := hfTestOpts(t, db, list)
	opts.GetSettings = func(ctx context.Context) (SystemSettings, error) {
		return SystemSettings{CacheTotalGB: 20}, nil
	}
	r := chi.NewRouter()
	r.Post("/api/huggingface/preheat", huggingFacePreheatHandler(opts))

	req := httptest.NewRequest("POST", "/api/huggingface/preheat",
		bytes.NewBufferString(`{"modelId":"big/model","force":true}`))
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusCreated {
		t.Fatalf("code=%d, body=%s", rr.Code, rr.Body.String())
	}
	var resp huggingFacePreheatResponse
	_ = json.Unmarshal(rr.Body.Bytes(), &resp)
	if resp.Refused {
		t.Errorf("Refused = true with force=true, want false")
	}
	if resp.Task.ID == 0 {
		t.Errorf("Task.ID = 0, want non-zero (task should be created)")
	}
}

func TestHuggingFacePreheat_NoCap_AllowsAll(t *testing.T) {
	dir := t.TempDir()
	db, _ := storage.Open(context.Background(), dir+"/test.db")
	t.Cleanup(func() { _ = db.Close() })

	// 1GB 文件，cap=0 表示"未配置"，不阻拦
	big := []huggingFaceFile{{Type: "file", Path: "f.bin", Size: 1 << 30}}
	list := func(ctx context.Context, modelID, revision string) ([]huggingFaceFile, error) {
		return big, nil
	}
	opts := hfTestOpts(t, db, list)
	opts.GetSettings = func(ctx context.Context) (SystemSettings, error) {
		return SystemSettings{CacheTotalGB: 0}, nil
	}
	r := chi.NewRouter()
	r.Post("/api/huggingface/preheat", huggingFacePreheatHandler(opts))

	req := httptest.NewRequest("POST", "/api/huggingface/preheat",
		bytes.NewBufferString(`{"modelId":"m/repo"}`))
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusCreated {
		t.Fatalf("code=%d, body=%s", rr.Code, rr.Body.String())
	}
}

func TestPreheatTask_AllowsHuggingFaceModelKind(t *testing.T) {
	dir := t.TempDir()
	db, _ := storage.Open(context.Background(), dir+"/test.db")
	t.Cleanup(func() { _ = db.Close() })

	r := buildPreheatRouter(preheatOpts(db, nil, nil))
	body := bytes.NewBufferString(`{"name":"hf:foo/bar","kind":"huggingface_model","targets":["foo/bar|main|config.json"]}`)
	req := httptest.NewRequest("POST", "/api/preheat/tasks", body)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusCreated {
		t.Fatalf("code=%d body=%s", rr.Code, rr.Body.String())
	}
}
