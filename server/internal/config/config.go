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
type Config struct {
	// HTTPAddr HTTP 监听地址，例如 ":8080" / "0.0.0.0:8080" / "127.0.0.1:9090"。
	HTTPAddr string
	// DataDir 数据目录，SQLite DB 文件位于 ${DataDir}/cncachehub.db。
	DataDir string
	// LogLevel 日志等级。
	LogLevel string
	// AdminPassword 控制台管理员密码（首版不用，但留好字段）。空字符串表示未设置。
	AdminPassword string
	// ShutdownTimeout graceful shutdown 最大等待时间。
	ShutdownTimeout time.Duration
	// DBPath 派生字段：${DataDir}/cncachehub.db。
	DBPath string
	// StartTime 派生字段：加载配置完成时的本地时间，main 启动后用于算 uptime。
	StartTime time.Time
}

// Default 返回默认值。不会被 Load() 自动使用，仅做文档与测试参考。
func Default() Config {
	return Config{
		HTTPAddr:        ":8080",
		DataDir:         "./data",
		LogLevel:        "info",
		AdminPassword:   "",
		ShutdownTimeout: 30 * time.Second,
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

	// 基础字段。
	c.HTTPAddr = getenv(EnvPrefix+"HTTP_ADDR", c.HTTPAddr)
	c.DataDir = getenv(EnvPrefix+"DATA_DIR", c.DataDir)
	c.LogLevel = strings.ToLower(getenv(EnvPrefix+"LOG_LEVEL", c.LogLevel))
	c.AdminPassword = os.Getenv(EnvPrefix + "ADMIN_PASSWORD") // 不设默认值；空 = 未配置

	// Shutdown timeout，单位秒，默认 30。
	if raw := os.Getenv(EnvPrefix + "SHUTDOWN_TIMEOUT_SECONDS"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n <= 0 {
			return Config{}, fmt.Errorf("invalid %sSHUTDOWN_TIMEOUT_SECONDS=%q: %w", EnvPrefix, raw, errInvalidShutdownTimeout)
		}
		c.ShutdownTimeout = time.Duration(n) * time.Second
	}

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
