// Package log 提供统一的日志封装。
//
// 设计原则：
//   - 基于标准库 log/slog，JSON handler 写到 stdout；
//   - 支持 level 过滤（debug / info / warn / error）；
//   - 自动脱敏敏感字段（password / token / secret / api_key / cookie 等）；
//   - 不在业务代码里直接 import slog，统一走本包方法，便于以后切底层实现。
package log

import (
	"context"
	"io"
	"log/slog"
	"os"
	"strings"
	"sync"
)

// Level 重导出 slog.Level，避免业务包直接 import slog。
type Level = slog.Level

// 业务层可用的 level 快捷常量。
const (
	LevelDebug = slog.LevelDebug
	LevelInfo  = slog.LevelInfo
	LevelWarn  = slog.LevelWarn
	LevelError = slog.LevelError
)

// sensitiveKeySubstrings 触发脱敏的字段名子串（大小写不敏感、匹配整个 key）。
// 命中规则：key 中包含任一子串即视为敏感字段。覆盖常见命名习惯（password / adminPassword / api_key / accessToken / ...）。
var sensitiveKeySubstrings = []string{
	"password",
	"passwd",
	"token",
	"secret",
	"api_key",
	"apikey",
	"cookie",
	"authorization",
	"credential",
}

// redactedValue 是脱敏后的占位值，避免泄露明文。
const redactedValue = "***"

// Options 控制 logger 初始化。
type Options struct {
	// Level 日志等级，nil 表示 info。
	Level *slog.Level
	// Writer 输出目标，nil 表示 stdout。
	Writer io.Writer
	// AddSource 是否在日志中包含源码位置（debug 时很有用，但有性能开销）。
	AddSource bool
}

// global 保护 default logger 的原子替换。
var (
	globalMu sync.RWMutex
	globalL  *slog.Logger = slog.New(newRedactingHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
)

// init 强制设置 default slog logger 与 globalL 一致，
// 这样第三方库如果绕过本包直接用 slog.Default() 也会走脱敏。
func init() {
	slog.SetDefault(globalL)
}

// Init 初始化全局 logger。重复调用会替换旧 logger。
func Init(opts Options) {
	w := opts.Writer
	if w == nil {
		w = os.Stdout
	}
	lvl := slog.LevelInfo
	if opts.Level != nil {
		lvl = *opts.Level
	}
	h := newRedactingHandler(w, &slog.HandlerOptions{
		Level:       lvl,
		AddSource:   opts.AddSource,
		ReplaceAttr: nil, // 我们在 redactingHandler 内部统一处理
	})
	l := slog.New(h)
	globalMu.Lock()
	globalL = l
	globalMu.Unlock()
	slog.SetDefault(l)
}

// L 返回当前全局 logger。调用方应使用包级 Info/Warn/Error/Debug 便捷方法，
// 仅在需要 With(...) 等场景下使用 L()。
func L() *slog.Logger {
	globalMu.RLock()
	defer globalMu.RUnlock()
	return globalL
}

// With 返回一个带附加字段的 logger。不会修改全局。
func With(args ...any) *slog.Logger {
	return L().With(args...)
}

// Debug / Info / Warn / Error 是包级便捷方法。
func Debug(msg string, args ...any) { L().Debug(msg, args...) }
func Info(msg string, args ...any)  { L().Info(msg, args...) }
func Warn(msg string, args ...any)  { L().Warn(msg, args...) }
func Error(msg string, args ...any) { L().Error(msg, args...) }

// DebugContext / InfoContext / WarnContext / ErrorContext 携带 context 变体。
func DebugContext(ctx context.Context, msg string, args ...any) {
	L().DebugContext(ctx, msg, args...)
}
func InfoContext(ctx context.Context, msg string, args ...any) {
	L().InfoContext(ctx, msg, args...)
}
func WarnContext(ctx context.Context, msg string, args ...any) {
	L().WarnContext(ctx, msg, args...)
}
func ErrorContext(ctx context.Context, msg string, args ...any) {
	L().ErrorContext(ctx, msg, args...)
}

// IsSensitive 判断字段名是否属于敏感字段。
// 比较是大小写不敏感的，包含匹配（任一子串命中即视为敏感）。
func IsSensitive(key string) bool {
	k := strings.ToLower(key)
	for _, sub := range sensitiveKeySubstrings {
		if strings.Contains(k, sub) {
			return true
		}
	}
	return false
}

// RedactValue 对外暴露的脱敏函数：把任意值脱敏为 "***"。
// 用于打印配置等结构体的场景。
func RedactValue(_ any) string {
	return redactedValue
}

// redactingHandler 包装任意 slog.Handler，扫描每条记录的 attr，
// 把敏感字段的值替换为 "***"。处理嵌套 group。
type redactingHandler struct {
	inner slog.Handler
}

// newRedactingHandler 构造一个脱敏 handler。
func newRedactingHandler(w io.Writer, opts *slog.HandlerOptions) slog.Handler {
	inner := slog.NewJSONHandler(w, opts)
	return &redactingHandler{inner: inner}
}

func (h *redactingHandler) Enabled(ctx context.Context, l slog.Level) bool {
	return h.inner.Enabled(ctx, l)
}

func (h *redactingHandler) Handle(ctx context.Context, r slog.Record) error {
	// 复制一份 attr，逐个脱敏后传给 inner handler。
	filtered := make([]slog.Attr, 0, r.NumAttrs())
	r.Attrs(func(a slog.Attr) bool {
		filtered = append(filtered, redactAttr(a))
		return true
	})
	// 重新组装 Record（保留原有字段）。
	nr := slog.NewRecord(r.Time, r.Level, r.Message, r.PC)
	nr.AddAttrs(filtered...)
	return h.inner.Handle(ctx, nr)
}

func (h *redactingHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	out := make([]slog.Attr, 0, len(attrs))
	for _, a := range attrs {
		out = append(out, redactAttr(a))
	}
	return &redactingHandler{inner: h.inner.WithAttrs(out)}
}

func (h *redactingHandler) WithGroup(name string) slog.Handler {
	return &redactingHandler{inner: h.inner.WithGroup(name)}
}

// redactAttr 递归脱敏 attr。group 的 children 也会被脱敏。
func redactAttr(a slog.Attr) slog.Attr {
	if IsSensitive(a.Key) {
		// 保留 Key，但把 Value 替换为字符串 "***"。
		return slog.String(a.Key, redactedValue)
	}
	if a.Value.Kind() == slog.KindGroup {
		// 递归处理 group 子字段。
		children := a.Value.Group()
		out := make([]slog.Attr, 0, len(children))
		for _, c := range children {
			out = append(out, redactAttr(c))
		}
		return slog.Attr{Key: a.Key, Value: slog.GroupValue(out...)}
	}
	// slog.LogValuer：展开后再脱敏。
	if a.Value.Kind() == slog.KindLogValuer {
		if v := a.Value.LogValuer(); v != nil {
			return slog.Any(a.Key, redactValueRecursive(v))
		}
	}
	return a
}

// redactValueRecursive 对任意值做敏感字段脱敏。
// 主要用于 slog.LogValuer 的返回值。
func redactValueRecursive(v any) any {
	switch t := v.(type) {
	case slog.Attr:
		return redactAttr(t)
	case []slog.Attr:
		out := make([]slog.Attr, len(t))
		for i, a := range t {
			out[i] = redactAttr(a)
		}
		return out
	}
	return v
}
