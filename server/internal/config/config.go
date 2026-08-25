// Package config 从环境变量加载 CNCacheHub 的运行配置。
//
// 约定：所有变量都以 CNCH_ 为前缀，路径风格（CNCH_HTTP_ADDR / CNCH_DATA_DIR / ...）。
// 设计原则：
//   - 不在业务代码里直接 os.Getenv，统一通过本包访问；
//   - 启动时返回 error 而不是 panic，便于上层做决策；
//   - 任何敏感字段（AdminPassword）从不在日志里出现明文。
package config

import (
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"strconv"
	"strings"
	"time"

	logpkg "github.com/cncachehub/server/internal/log"
)

// EnvPrefix 是所有环境变量的公共前缀。
const EnvPrefix = "CNCH_"

// Config 是 CNCacheHub 运行时的完整配置。
//
// 字段按"基础 / 缓存 / 上游 / 小容量 VPS 优化"四组组织；
// 缺省值见 Default()。环境变量全部以 CNCH_ 为前缀。
type Config struct {
	// ===== 基础 =====
	// HTTPAddr HTTP 监听地址，例如 ":8080" / "0.0.0.0:8080" / "127.0.0.1:9090"。
	HTTPAddr string
	// DataDir 数据目录，SQLite DB 文件位于 ${DataDir}/cncachehub.db。
	DataDir string
	// LogDir 日志目录；保留字段（slog 当前走 stderr，未来文件日志用）。
	LogDir string
	// LogLevel 日志等级。
	LogLevel string
	// AdminPassword 控制台管理员密码（首版不用，但留好字段）。空字符串表示未设置。
	AdminPassword string
	// ShutdownTimeout graceful shutdown 最大等待时间。
	ShutdownTimeout time.Duration
	// TrustedProxies 逗号分隔的 CIDR 列表，标识哪些 RemoteAddr 来源可信任其
	// 设置的 X-Forwarded-For / X-Real-IP header。空 = 用默认（loopback + RFC1918）。
	// 部署在公网独立 nginx / Caddy / Cloudflare 后必须显式列出 proxy 的 CIDR/IP。
	TrustedProxies []string
	// DBPath 派生字段：${DataDir}/cncachehub.db。
	DBPath string
	// StartTime 派生字段：加载配置完成时的本地时间，main 启动后用于算 uptime。
	StartTime time.Time

	// ===== 缓存（Phase 1） =====
	// CacheDir blob 落盘根目录。
	// 路径布局：${CacheDir}/v2/${registry}/${repo}/blobs/${digest}
	CacheDir string

	// ===== 上游 Registry（Phase 1） =====
	// UpstreamRegistry 默认上游 Registry URL。
	// Docker Hub 是 https://registry-1.docker.io，私有仓库改这里。
	UpstreamRegistry string
	// UpstreamTimeout 上游 HTTP 请求总超时。
	UpstreamTimeout time.Duration

	// ===== 小容量 VPS 优化（PRD §9.1.4） =====
	// SmallVPSOpt 是否启用小容量 VPS 优化（影响日志详细度、并发数、bypass 严格度）。
	SmallVPSOpt bool
	// ReserveSpaceGB 缓存目录所在文件系统需保留的最小可用空间（GB）。
	// 低于此值时新请求走旁路（仍转发不缓存）。
	ReserveSpaceGB int
	// MaxObjectSizeMB 单个对象落盘上限（MB）。超过则旁路不缓存。
	MaxObjectSizeMB int
	// CacheTotalGB 缓存总配额上限（GB）。Phase 1 仅记录不强制，Phase 2 清理任务会用到。
	CacheTotalGB int
}

// Default 返回默认值。不会被 Load() 自动使用，仅做文档与测试参考。
func Default() Config {
	return Config{
		HTTPAddr:         ":8080",
		DataDir:          "./data",
		LogDir:           "./logs",
		LogLevel:         "info",
		AdminPassword:    "",
		ShutdownTimeout:  30 * time.Second,
		// TrustedProxies 默认空 → clientip 用内置白名单（loopback + RFC1918）。
		// 通过 CNCH_TRUSTED_PROXIES 环境变量覆盖。
		CacheDir:         "./cache",
		UpstreamRegistry: "https://registry-1.docker.io",
		UpstreamTimeout:  60 * time.Second,
		SmallVPSOpt:      false,
		ReserveSpaceGB:   5,
		MaxObjectSizeMB:  1024,
		CacheTotalGB:     20,
	}
}

// Load 从环境变量加载配置并做基础校验。
//
// 行为：
//   - 缺省值与 Default() 一致；
//   - 校验失败返回 error（不 panic）；
//   - 不会读取敏感字段以外未声明的变量，保持显式。
func Load() (Config, error) {
	c := Default()
	c.StartTime = time.Now()

	// ===== 基础 =====
	c.HTTPAddr = getenv(EnvPrefix+"HTTP_ADDR", c.HTTPAddr)
	c.DataDir = getenv(EnvPrefix+"DATA_DIR", c.DataDir)
	c.LogDir = getenv(EnvPrefix+"LOG_DIR", c.LogDir)
	c.LogLevel = strings.ToLower(getenv(EnvPrefix+"LOG_LEVEL", c.LogLevel))
	c.AdminPassword = os.Getenv(EnvPrefix + "ADMIN_PASSWORD") // 不设默认值；空 = 未配置
	// CNCH_TRUSTED_PROXIES：逗号分隔 CIDR 列表，标识哪些 RemoteAddr 来源可信任
	// 其 X-Forwarded-For / X-Real-IP header（否则忽略 header 防止 IP 伪造）。
	// 部署在公网独立 nginx / Caddy / Cloudflare 后必须显式列出。
	// 空 = 用 clientip 包内置默认（loopback + RFC1918）。
	if raw := os.Getenv(EnvPrefix + "TRUSTED_PROXIES"); raw != "" {
		for _, p := range strings.Split(raw, ",") {
			if p = strings.TrimSpace(p); p != "" {
				c.TrustedProxies = append(c.TrustedProxies, p)
			}
		}
	}

	// Shutdown timeout，单位秒，默认 30。
	if raw := os.Getenv(EnvPrefix + "SHUTDOWN_TIMEOUT_SECONDS"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n <= 0 {
			return Config{}, fmt.Errorf("invalid %sSHUTDOWN_TIMEOUT_SECONDS=%q: %w", EnvPrefix, raw, errInvalidShutdownTimeout)
		}
		c.ShutdownTimeout = time.Duration(n) * time.Second
	}

	// ===== 缓存 =====
	c.CacheDir = getenv(EnvPrefix+"CACHE_DIR", c.CacheDir)

	// ===== 上游 =====
	c.UpstreamRegistry = strings.TrimRight(getenv(EnvPrefix+"UPSTREAM_REGISTRY", c.UpstreamRegistry), "/")
	if raw := os.Getenv(EnvPrefix + "UPSTREAM_TIMEOUT_SECONDS"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n <= 0 {
			return Config{}, fmt.Errorf("invalid %sUPSTREAM_TIMEOUT_SECONDS=%q: %w", EnvPrefix, raw, errInvalidTimeout)
		}
		c.UpstreamTimeout = time.Duration(n) * time.Second
	}

	// ===== 小容量 VPS 优化 =====
	c.SmallVPSOpt = parseBool(os.Getenv(EnvPrefix + "SMALL_VPS_OPT"), c.SmallVPSOpt)
	c.ReserveSpaceGB = parseInt(os.Getenv(EnvPrefix+"RESERVE_SPACE_GB"), c.ReserveSpaceGB, 0, 10000)
	c.MaxObjectSizeMB = parseInt(os.Getenv(EnvPrefix+"MAX_OBJECT_SIZE_MB"), c.MaxObjectSizeMB, 1, 1024*1024)
	c.CacheTotalGB = parseInt(os.Getenv(EnvPrefix+"CACHE_TOTAL_GB"), c.CacheTotalGB, 0, 100000)

	// 校验。
	if err := c.validate(); err != nil {
		return Config{}, err
	}

	// 派生字段。
	c.DBPath = joinPath(c.DataDir, "cncachehub.db")
	return c, nil
}

// validate 做字段级校验。
func (c Config) validate() error {
	var errs []string

	if c.HTTPAddr == "" {
		errs = append(errs, "HTTP_ADDR is required")
	} else {
		// 允许 ":port" / "host:port" 形式。
		if _, _, err := net.SplitHostPort(c.HTTPAddr); err != nil {
			errs = append(errs, fmt.Sprintf("HTTP_ADDR %q is not a valid host:port", c.HTTPAddr))
		}
	}

	if c.DataDir == "" {
		errs = append(errs, "DATA_DIR is required")
	}
	if c.CacheDir == "" {
		errs = append(errs, "CACHE_DIR is required")
	}
	if c.UpstreamRegistry == "" {
		errs = append(errs, "UPSTREAM_REGISTRY is required")
	} else {
		u := c.UpstreamRegistry
		if !strings.HasPrefix(u, "http://") && !strings.HasPrefix(u, "https://") {
			errs = append(errs, fmt.Sprintf("UPSTREAM_REGISTRY %q must start with http:// or https://", u))
		}
	}
	if c.UpstreamTimeout <= 0 {
		errs = append(errs, "UPSTREAM_TIMEOUT_SECONDS must be > 0")
	}

	switch c.LogLevel {
	case "debug", "info", "warn", "error":
		// ok
	default:
		errs = append(errs, fmt.Sprintf("LOG_LEVEL %q must be one of debug/info/warn/error", c.LogLevel))
	}

	if c.ShutdownTimeout <= 0 {
		errs = append(errs, "SHUTDOWN_TIMEOUT_SECONDS must be > 0")
	}

	if len(errs) > 0 {
		return fmt.Errorf("invalid config: %s: %w", strings.Join(errs, "; "), ErrInvalidConfig)
	}
	return nil
}

// ErrInvalidConfig 表示配置校验失败。
var ErrInvalidConfig = errors.New("invalid config")

// errInvalidShutdownTimeout 是 shutdown timeout 解析失败的哨兵错误。
var errInvalidShutdownTimeout = errors.New("invalid shutdown timeout")

// errInvalidTimeout 是 upstream timeout 解析失败的哨兵错误。
var errInvalidTimeout = errors.New("invalid timeout")

// IsInvalidConfig 判断 err 是否为 ErrInvalidConfig（或包装）。
func IsInvalidConfig(err error) bool {
	return errors.Is(err, ErrInvalidConfig)
}

// LogValue 实现 slog.LogValuer 接口：打印配置时自动脱敏 AdminPassword。
//
// 注意：slog 不会自动调用 LogValuer；脱敏由 internal/log 的 redactingHandler 完成。
// 这里仍然实现 LogValuer 以便其他场景（pflag.Stringer 等）安全打印。
func (c Config) LogValue() slog.Value { return slog.GroupValue() }

// getenv 返回环境变量值，若为空则回退到 def。
func getenv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// parseBool 解析 bool 环境变量。空 / 无法识别回退到 def。
// 接受: 1, t, T, TRUE, true, True, 0, f, F, FALSE, false, False。
func parseBool(raw string, def bool) bool {
	if raw == "" {
		return def
	}
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "1", "t", "true", "yes", "y", "on":
		return true
	case "0", "f", "false", "no", "n", "off":
		return false
	default:
		return def
	}
}

// parseInt 解析 int 环境变量。空 / 越界回退到 def。
func parseInt(raw string, def, min, max int) int {
	if raw == "" {
		return def
	}
	n, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil {
		return def
	}
	if n < min || n > max {
		return def
	}
	return n
}

// joinPath 跨平台路径拼接（仅用于本包派生字段，不处理 ~ / 环境变量）。
func joinPath(dir, name string) string {
	sep := string(os.PathSeparator)
	if strings.HasSuffix(dir, sep) {
		return dir + name
	}
	return dir + sep + name
}

// InitLogger 根据 cfg.LogLevel 初始化全局 logger。
func (c Config) InitLogger() {
	lvl := logpkg.LevelInfo
	switch c.LogLevel {
	case "debug":
		lvl = logpkg.LevelDebug
	case "info":
		lvl = logpkg.LevelInfo
	case "warn":
		lvl = logpkg.LevelWarn
	case "error":
		lvl = logpkg.LevelError
	}
	logpkg.Init(logpkg.Options{Level: &lvl})
}
