package config

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"

	logpkg "github.com/cncachehub/server/internal/log"
)

// clearEnv 清理本测试关心的所有 CNCH_ 变量。
func clearEnv(t *testing.T) {
	t.Helper()
	t.Setenv("CNCH_HTTP_ADDR", "")
	t.Setenv("CNCH_DATA_DIR", "")
	t.Setenv("CNCH_LOG_DIR", "")
	t.Setenv("CNCH_LOG_LEVEL", "")
	t.Setenv("CNCH_ADMIN_PASSWORD", "")
	t.Setenv("CNCH_SHUTDOWN_TIMEOUT_SECONDS", "")
	t.Setenv("CNCH_CACHE_DIR", "")
	t.Setenv("CNCH_UPSTREAM_REGISTRY", "")
	t.Setenv("CNCH_UPSTREAM_TIMEOUT_SECONDS", "")
	t.Setenv("CNCH_SMALL_VPS_OPT", "")
	t.Setenv("CNCH_RESERVE_SPACE_GB", "")
	t.Setenv("CNCH_MAX_OBJECT_SIZE_MB", "")
	t.Setenv("CNCH_CACHE_TOTAL_GB", "")
}

// TestLoad_Defaults 验证全部使用默认值。
func TestLoad_Defaults(t *testing.T) {
	clearEnv(t)
	c, err := Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if c.HTTPAddr != ":8080" {
		t.Errorf("HTTPAddr = %q, want :8080", c.HTTPAddr)
	}
	if c.DataDir != "./data" {
		t.Errorf("DataDir = %q, want ./data", c.DataDir)
	}
	if c.LogLevel != "info" {
		t.Errorf("LogLevel = %q, want info", c.LogLevel)
	}
	if c.AdminPassword != "" {
		t.Errorf("AdminPassword = %q, want empty", c.AdminPassword)
	}
	if c.ShutdownTimeout.Seconds() != 30 {
		t.Errorf("ShutdownTimeout = %v, want 30s", c.ShutdownTimeout)
	}
	if c.DBPath != "data/cncachehub.db" && c.DBPath != "./data/cncachehub.db" {
		// 允许两种：取决于 OS separator；这里宽容匹配。
		if !strings.HasSuffix(c.DBPath, "cncachehub.db") {
			t.Errorf("DBPath = %q, want suffix cncachehub.db", c.DBPath)
		}
	}
	// Phase 1 新字段默认
	if c.CacheDir != "./cache" {
		t.Errorf("CacheDir = %q, want ./cache", c.CacheDir)
	}
	if c.UpstreamRegistry != "https://registry-1.docker.io" {
		t.Errorf("UpstreamRegistry = %q", c.UpstreamRegistry)
	}
	if c.UpstreamTimeout.Seconds() != 60 {
		t.Errorf("UpstreamTimeout = %v, want 60s", c.UpstreamTimeout)
	}
	if c.SmallVPSOpt != false {
		t.Errorf("SmallVPSOpt = %v, want false", c.SmallVPSOpt)
	}
	if c.ReserveSpaceGB != 5 {
		t.Errorf("ReserveSpaceGB = %d, want 5", c.ReserveSpaceGB)
	}
	if c.MaxObjectSizeMB != 1024 {
		t.Errorf("MaxObjectSizeMB = %d, want 1024", c.MaxObjectSizeMB)
	}
	if c.CacheTotalGB != 20 {
		t.Errorf("CacheTotalGB = %d, want 20", c.CacheTotalGB)
	}
}

// TestLoad_Overrides 验证环境变量覆盖默认值。
func TestLoad_Overrides(t *testing.T) {
	clearEnv(t)
	t.Setenv("CNCH_HTTP_ADDR", "127.0.0.1:9090")
	t.Setenv("CNCH_DATA_DIR", "/var/lib/cnch")
	t.Setenv("CNCH_LOG_LEVEL", "debug")
	t.Setenv("CNCH_ADMIN_PASSWORD", "sup3r-secret")
	t.Setenv("CNCH_SHUTDOWN_TIMEOUT_SECONDS", "10")

	c, err := Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if c.HTTPAddr != "127.0.0.1:9090" {
		t.Errorf("HTTPAddr = %q", c.HTTPAddr)
	}
	if c.DataDir != "/var/lib/cnch" {
		t.Errorf("DataDir = %q", c.DataDir)
	}
	if c.LogLevel != "debug" {
		t.Errorf("LogLevel = %q", c.LogLevel)
	}
	if c.AdminPassword != "sup3r-secret" {
		t.Errorf("AdminPassword = %q", c.AdminPassword)
	}
	if c.ShutdownTimeout.Seconds() != 10 {
		t.Errorf("ShutdownTimeout = %v", c.ShutdownTimeout)
	}
}

// TestLoad_InvalidLogLevel 验证非法日志等级报错。
func TestLoad_InvalidLogLevel(t *testing.T) {
	clearEnv(t)
	t.Setenv("CNCH_LOG_LEVEL", "verbose")
	_, err := Load()
	if err == nil {
		t.Fatal("expected error for invalid LOG_LEVEL")
	}
	if !IsInvalidConfig(err) {
		t.Errorf("expected ErrInvalidConfig, got: %v", err)
	}
}

// TestLoad_InvalidHTTPAddr 验证非法监听地址报错。
func TestLoad_InvalidHTTPAddr(t *testing.T) {
	clearEnv(t)
	t.Setenv("CNCH_HTTP_ADDR", "no-port-here")
	_, err := Load()
	if err == nil {
		t.Fatal("expected error for invalid HTTP_ADDR")
	}
	if !IsInvalidConfig(err) {
		t.Errorf("expected ErrInvalidConfig, got: %v", err)
	}
}

// TestLoad_InvalidShutdownTimeout 验证非法 shutdown timeout 报错。
func TestLoad_InvalidShutdownTimeout(t *testing.T) {
	clearEnv(t)
	t.Setenv("CNCH_SHUTDOWN_TIMEOUT_SECONDS", "abc")
	_, err := Load()
	if err == nil {
		t.Fatal("expected error for invalid SHUTDOWN_TIMEOUT_SECONDS")
	}
}

// TestLoad_NegativeShutdownTimeout 验证负数 timeout 报错。
func TestLoad_NegativeShutdownTimeout(t *testing.T) {
	clearEnv(t)
	t.Setenv("CNCH_SHUTDOWN_TIMEOUT_SECONDS", "-1")
	_, err := Load()
	if err == nil {
		t.Fatal("expected error for negative SHUTDOWN_TIMEOUT_SECONDS")
	}
}

// TestLogLevelCaseInsensitive 验证 LOG_LEVEL 大小写不敏感。
func TestLogLevelCaseInsensitive(t *testing.T) {
	clearEnv(t)
	t.Setenv("CNCH_LOG_LEVEL", "DEBUG")
	c, err := Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if c.LogLevel != "debug" {
		t.Errorf("LogLevel = %q, want debug", c.LogLevel)
	}
}

// TestLoad_Phase1_Overrides 验证 Phase 1 新字段的环境变量覆盖。
func TestLoad_Phase1_Overrides(t *testing.T) {
	clearEnv(t)
	t.Setenv("CNCH_CACHE_DIR", "/var/cache/cnch")
	t.Setenv("CNCH_UPSTREAM_REGISTRY", "https://my-mirror.example.com/")
	t.Setenv("CNCH_UPSTREAM_TIMEOUT_SECONDS", "120")
	t.Setenv("CNCH_SMALL_VPS_OPT", "true")
	t.Setenv("CNCH_RESERVE_SPACE_GB", "10")
	t.Setenv("CNCH_MAX_OBJECT_SIZE_MB", "2048")
	t.Setenv("CNCH_CACHE_TOTAL_GB", "100")

	c, err := Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if c.CacheDir != "/var/cache/cnch" {
		t.Errorf("CacheDir = %q", c.CacheDir)
	}
	if c.UpstreamRegistry != "https://my-mirror.example.com" {
		// 末尾 / 应被 TrimRight
		t.Errorf("UpstreamRegistry = %q, want trailing slash trimmed", c.UpstreamRegistry)
	}
	if c.UpstreamTimeout.Seconds() != 120 {
		t.Errorf("UpstreamTimeout = %v, want 120s", c.UpstreamTimeout)
	}
	if c.SmallVPSOpt != true {
		t.Errorf("SmallVPSOpt = %v, want true", c.SmallVPSOpt)
	}
	if c.ReserveSpaceGB != 10 {
		t.Errorf("ReserveSpaceGB = %d, want 10", c.ReserveSpaceGB)
	}
	if c.MaxObjectSizeMB != 2048 {
		t.Errorf("MaxObjectSizeMB = %d, want 2048", c.MaxObjectSizeMB)
	}
	if c.CacheTotalGB != 100 {
		t.Errorf("CacheTotalGB = %d, want 100", c.CacheTotalGB)
	}
}

// TestLoad_InvalidUpstream 验证非法 upstream URL 报错。
func TestLoad_InvalidUpstream(t *testing.T) {
	clearEnv(t)
	t.Setenv("CNCH_UPSTREAM_REGISTRY", "not-a-url")
	_, err := Load()
	if err == nil {
		t.Fatal("expected error for invalid UPSTREAM_REGISTRY")
	}
	if !IsInvalidConfig(err) {
		t.Errorf("expected ErrInvalidConfig, got: %v", err)
	}
}

// TestLoad_InvalidUpstreamTimeout 验证非法 upstream timeout 报错。
func TestLoad_InvalidUpstreamTimeout(t *testing.T) {
	clearEnv(t)
	t.Setenv("CNCH_UPSTREAM_TIMEOUT_SECONDS", "abc")
	_, err := Load()
	if err == nil {
		t.Fatal("expected error for invalid UPSTREAM_TIMEOUT_SECONDS")
	}
}

// TestLoad_SmallVPSOptVariants 验证 bool 解析的多种写法。
func TestLoad_SmallVPSOptVariants(t *testing.T) {
	cases := []struct {
		raw  string
		want bool
	}{
		{"true", true},
		{"TRUE", true},
		{"1", true},
		{"yes", true},
		{"on", true},
		{"false", false},
		{"0", false},
		{"no", false},
		{"off", false},
		{"", false},  // 空字符串 → 默认 false
		{"garbage", false}, // 解析失败 → 默认 false
	}
	for _, tc := range cases {
		t.Run(tc.raw, func(t *testing.T) {
			clearEnv(t)
			t.Setenv("CNCH_SMALL_VPS_OPT", tc.raw)
			c, err := Load()
			if err != nil {
				t.Fatalf("Load() error: %v", err)
			}
			if c.SmallVPSOpt != tc.want {
				t.Errorf("SmallVPSOpt = %v, want %v (raw=%q)", c.SmallVPSOpt, tc.want, tc.raw)
			}
		})
	}
}

// TestLoad_OutOfRangeInt 验证越界 int 走默认值。
func TestLoad_OutOfRangeInt(t *testing.T) {
	clearEnv(t)
	t.Setenv("CNCH_RESERVE_SPACE_GB", "-1") // min=0, 越界
	c, err := Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if c.ReserveSpaceGB != 5 { // 默认值
		t.Errorf("ReserveSpaceGB = %d, want 5 (default after OOR)", c.ReserveSpaceGB)
	}
}

// TestInitLogger_NoLeakPassword 验证配置日志中 admin_password 被脱敏。
func TestInitLogger_NoLeakPassword(t *testing.T) {
	clearEnv(t)
	t.Setenv("CNCH_ADMIN_PASSWORD", "topsecret-do-not-leak")

	c, err := Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	// 用一个 buffer 接住日志。
	var buf bytes.Buffer
	debug := slog.LevelDebug
	logpkg.Init(logpkg.Options{Level: &debug, Writer: &buf})
	// 重新初始化 config logger（确保用同一个 buffer）
	c.InitLogger()
	// Init 之后需要再次设置 writer，因为我们 InitLogger 会重置 stdout；这里覆盖。
	logpkg.Init(logpkg.Options{Level: &debug, Writer: &buf})

	// 模拟 main.go 的"config loaded"日志
	logpkg.Info("config loaded",
		"http_addr", c.HTTPAddr,
		"data_dir", c.DataDir,
		"log_level", c.LogLevel,
		"admin_password", c.AdminPassword,
	)

	out := buf.String()
	if strings.Contains(out, "topsecret-do-not-leak") {
		t.Fatalf("admin_password leaked in log output:\n%s", out)
	}
	if !strings.Contains(out, `"admin_password":"***"`) {
		t.Fatalf("expected admin_password to be redacted, got:\n%s", out)
	}

	// 解析至少一条 JSON 行确认格式正确。
	var found bool
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(line), &m); err == nil {
			if m["msg"] == "config loaded" {
				found = true
			}
		}
	}
	if !found {
		t.Fatalf("expected to find a 'config loaded' log line, got:\n%s", out)
	}
}
