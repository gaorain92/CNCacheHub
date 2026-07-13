package log

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"sync"
	"testing"
)

// TestIsSensitive 覆盖所有已知敏感字段及其大小写、变体。
func TestIsSensitive(t *testing.T) {
	cases := []struct {
		key   string
		want  bool
	}{
		{"password", true},
		{"Password", true},
		{"admin_password", true},
		{"userPasswd", true},
		{"token", true},
		{"access_token", true},
		{"refreshToken", true},
		{"secret", true},
		{"client_secret", true},
		{"api_key", true},
		{"apikey", true},
		{"API_KEY", true},
		{"cookie", true},
		{"set_cookie", true},
		{"authorization", true},
		{"Credential", true},

		// 非敏感字段
		{"username", false},
		{"user", false},
		{"path", false},
		{"status", false},
		{"duration_ms", false},
		{"request_id", false},
	}
	for _, c := range cases {
		if got := IsSensitive(c.key); got != c.want {
			t.Errorf("IsSensitive(%q) = %v, want %v", c.key, got, c.want)
		}
	}
}

// TestRedactValue 简单验证占位值。
func TestRedactValue(t *testing.T) {
	if got := RedactValue("anything"); got != redactedValue {
		t.Errorf("RedactValue = %q, want %q", got, redactedValue)
	}
}

// TestRedactingHandler_TopLevel 检查顶层敏感字段被脱敏。
func TestRedactingHandler_TopLevel(t *testing.T) {
	var buf bytes.Buffer
	h := newRedactingHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})
	l := slog.New(h)

	l.Info("login attempt", "username", "alice", "password", "hunter2", "token", "abc.def.ghi")

	out := buf.String()
	if !strings.Contains(out, `"password":"`+redactedValue+`"`) {
		t.Errorf("expected password to be redacted, got: %s", out)
	}
	if !strings.Contains(out, `"token":"`+redactedValue+`"`) {
		t.Errorf("expected token to be redacted, got: %s", out)
	}
	if !strings.Contains(out, `"username":"alice"`) {
		t.Errorf("username should pass through, got: %s", out)
	}
	if strings.Contains(out, "hunter2") {
		t.Errorf("password plaintext leaked: %s", out)
	}
	if strings.Contains(out, "abc.def.ghi") {
		t.Errorf("token plaintext leaked: %s", out)
	}
}

// TestRedactingHandler_Group 验证 group 内的敏感字段也被脱敏。
func TestRedactingHandler_Group(t *testing.T) {
	var buf bytes.Buffer
	h := newRedactingHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})
	l := slog.New(h)

	l.Info("db connect", "host", "127.0.0.1", "creds", slog.GroupValue(
		slog.String("user", "root"),
		slog.String("password", "toor"),
		slog.String("api_key", "sk-test"),
	))

	out := buf.String()
	if !strings.Contains(out, `"password":"`+redactedValue+`"`) {
		t.Errorf("expected nested password to be redacted, got: %s", out)
	}
	if !strings.Contains(out, `"api_key":"`+redactedValue+`"`) {
		t.Errorf("expected nested api_key to be redacted, got: %s", out)
	}
	if !strings.Contains(out, `"user":"root"`) {
		t.Errorf("user should pass through, got: %s", out)
	}
	if strings.Contains(out, "toor") {
		t.Errorf("password plaintext leaked: %s", out)
	}
	if strings.Contains(out, "sk-test") {
		t.Errorf("api_key plaintext leaked: %s", out)
	}
}

// TestInit_ReplacesGlobalLogger 验证 Init 替换全局 logger。
func TestInit_ReplacesGlobalLogger(t *testing.T) {
	var buf bytes.Buffer
	Init(Options{Writer: &buf})
	// 触发一次写入。
	Info("hello", "password", "secret123")

	// 因为我们替换了 globalL，但要等 slog.SetDefault 同步；这里直接读 buf。
	if !strings.Contains(buf.String(), `"password":"`+redactedValue+`"`) {
		t.Errorf("expected redacted output, got: %s", buf.String())
	}
}

// TestConcurrentInitRace 验证 Init 在并发场景下不 panic。
// 用 -race 跑能发现 data race。
func TestConcurrentInitRace(t *testing.T) {
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			Init(Options{Writer: &bytes.Buffer{}})
			Info("hi", "password", "x")
			_ = L()
		}()
	}
	wg.Wait()
}

// TestContextVariants 验证 *Context 方法不 panic。
func TestContextVariants(t *testing.T) {
	var buf bytes.Buffer
	Init(Options{Writer: &buf})
	ctx := context.Background()
	DebugContext(ctx, "d", "password", "p")
	InfoContext(ctx, "i", "password", "p")
	WarnContext(ctx, "w", "password", "p")
	ErrorContext(ctx, "e", "password", "p")
	// sanity: 至少要脱敏
	if strings.Contains(buf.String(), `"password":"p"`) {
		t.Errorf("password plaintext leaked in context variants: %s", buf.String())
	}
}

// TestJSONOutput 简单验证输出是合法 JSON（每行一个 record）。
func TestJSONOutput(t *testing.T) {
	var buf bytes.Buffer
	Init(Options{Writer: &buf})
	Info("hi")
	line := strings.TrimSpace(buf.String())
	var m map[string]any
	if err := json.Unmarshal([]byte(line), &m); err != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", err, line)
	}
	if m["msg"] != "hi" {
		t.Errorf("expected msg=hi, got: %v", m)
	}
}
