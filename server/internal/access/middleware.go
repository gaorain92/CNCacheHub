// Package access 实现代理访问控制（PRD §9.7.2 P2#4）。
//
// 支持两种凭据：
//   - Bearer Token：X-CNCacheHub-Token: <secret> 或 Authorization: Bearer <secret>
//   - IP 白名单：逗号分隔 CIDR 列表（10.0.0.0/8, 192.168.0.0/16, ::1/128）
//
// 行为：
//   - Enabled = false → 完全放行（默认）
//   - Enabled = true：
//     - loopback + LoopbackBypass=true → 放行（默认开，方便 docker 镜像里 curl localhost）
//     - 任一凭据通过（token 匹配 OR IP 在白名单中） → 放行
//     - 否则 401
//
// 凭据在调用方用 GetConfig 动态读（admin 改后立刻生效）。
package access

import (
	"net"
	"net/http"
	"strings"
	"sync"
)

// Config 是访问控制当前配置。值类型，每次 reload 整个替换。
type Config struct {
	Enabled        bool
	Token          string
	IPWhitelist    []string
	LoopbackBypass bool
}

// IsEmpty 报告配置是否等于"未启用"（UI 显示"未保护"用）。
func (c Config) IsEmpty() bool {
	return !c.Enabled
}

// String 返回脱敏后的 token（用于日志/UI）。
func (c Config) String() string {
	masked := "***"
	if c.Token == "" {
		masked = "(empty)"
	}
	return "Config{Enabled=" + boolStr(c.Enabled) +
		", Token=" + masked +
		", IPWhitelist=" + joinCIDR(c.IPWhitelist) +
		", LoopbackBypass=" + boolStr(c.LoopbackBypass) + "}"
}

func boolStr(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

func joinCIDR(cidrs []string) string {
	if len(cidrs) == 0 {
		return "(empty)"
	}
	return strings.Join(cidrs, ",")
}

// Resolver 抽象配置来源 — main.go 注入一个 closure 从 DB 读最新值。
type Resolver func() Config

// Middleware 构造一个 access control 中间件。
//
// 用法：r.With(access.Middleware(getConfig)).Handle("/v2/*", proxyHandler)
//
// 注意：getConfig 在每个请求时调一次（开销就是读 4 行 SQLite，可忽略）；
// 如果未来要严格一致可以加 atomic.Pointer，但目前没必要。
func Middleware(resolve Resolver) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			cfg := resolve()
			if !cfg.Enabled {
				next.ServeHTTP(w, r)
				return
			}

			clientIP := realClientIP(r)

			// 1. loopback bypass
			if cfg.LoopbackBypass && isLoopback(clientIP) {
				next.ServeHTTP(w, r)
				return
			}

			// 2. token check
			if cfg.Token != "" {
				tok := extractToken(r)
				if tok != "" && constantTimeEqual(tok, cfg.Token) {
					next.ServeHTTP(w, r)
					return
				}
			}

			// 3. IP whitelist check
			if len(cfg.IPWhitelist) > 0 {
				ip := net.ParseIP(clientIP)
				if ip != nil && ipMatchesCIDRs(ip, cfg.IPWhitelist) {
					next.ServeHTTP(w, r)
					return
				}
			}

			// 全部失败
			w.Header().Set("WWW-Authenticate", `Bearer realm="cncachehub-proxy"`)
			http.Error(w, "access denied: invalid token or ip", http.StatusUnauthorized)
		})
	}
}

// === helpers ===

// realClientIP 拿真实客户端 IP（先看 X-Forwarded-For / X-Real-IP，最后 RemoteAddr）。
//
// 简化：信任 proxy header（部署在 nginx / Caddy 后由它们填）。
// 如果是公网直连，RemoteAddr 是真实 IP。
func realClientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		// 取第一个 IP（最近客户端）
		if i := strings.Index(xff, ","); i >= 0 {
			return strings.TrimSpace(xff[:i])
		}
		return strings.TrimSpace(xff)
	}
	if xri := r.Header.Get("X-Real-IP"); xri != "" {
		return strings.TrimSpace(xri)
	}
	// RemoteAddr 是 "IP:port"
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// extractToken 从 header 拿 token。
//
// 优先级：X-CNCacheHub-Token > Authorization: Bearer
func extractToken(r *http.Request) string {
	if t := r.Header.Get("X-CNCacheHub-Token"); t != "" {
		return t
	}
	if auth := r.Header.Get("Authorization"); auth != "" {
		const prefix = "Bearer "
		if strings.HasPrefix(auth, prefix) {
			return strings.TrimSpace(auth[len(prefix):])
		}
	}
	// query 不接受（避免 access log 泄露）
	return ""
}

// constantTimeEqual 简单 constant-time 比较（防 timing attack）。
func constantTimeEqual(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	var diff byte
	for i := 0; i < len(a); i++ {
		diff |= a[i] ^ b[i]
	}
	return diff == 0
}

// isLoopback 报告 ip 字符串是否是 loopback（127.0.0.0/8 或 ::1）。
//
// 同时支持精确字符串匹配（无 net.ParseIP 开销）。
func isLoopback(ipStr string) bool {
	ip := net.ParseIP(ipStr)
	if ip == nil {
		return false
	}
	return ip.IsLoopback()
}

// ipMatchesCIDRs 报告 ip 是否在任一 CIDR 范围内。
func ipMatchesCIDRs(ip net.IP, cidrs []string) bool {
	for _, c := range cidrs {
		_, n, err := net.ParseCIDR(c)
		if err != nil {
			continue
		}
		if n.Contains(ip) {
			return true
		}
	}
	return false
}

// ParseCIDRList 把逗号分隔的字符串切成 []CIDR，跳过空 + 错。
func ParseCIDRList(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		// 验证合法性
		if _, _, err := net.ParseCIDR(p); err == nil {
			out = append(out, p)
		}
		// 错误 CIDR 静默跳过（输入校验是 UI 责任）
	}
	return out
}

// StaticResolver 把固定 config 包成 Resolver（用于测试）。
func StaticResolver(c Config) Resolver {
	var mu sync.RWMutex
	cur := c
	return func() Config {
		mu.RLock()
		defer mu.RUnlock()
		return cur
	}
}

// MutableResolver 返回一个可以 Set 的 Resolver（运行时 reload 用）。
func MutableResolver(initial Config) (Resolver, func(Config)) {
	var mu sync.RWMutex
	cur := initial
	get := func() Config {
		mu.RLock()
		defer mu.RUnlock()
		return cur
	}
	set := func(c Config) {
		mu.Lock()
		defer mu.Unlock()
		cur = c
	}
	return get, set
}
