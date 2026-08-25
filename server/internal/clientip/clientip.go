// Package clientip 提供安全的客户端 IP 提取。
//
// 安全要点：不能无条件信任 X-Forwarded-For — 否则攻击者直接连到 Go 服务
// （绕过 nginx）时，可通过 `X-Forwarded-For: 1.2.3.4` 伪造 IP 绕过：
//   - 速率限制（按 IP 限流）
//   - IP 白名单 access control
//   - audit log 中的 IP 溯源
//
// 规则：只有当 r.RemoteAddr 在 "trusted proxy" 范围内时，才用 X-Forwarded-For。
// 默认 trusted = loopback + RFC1918（10/8, 172.16/12, 192.168/16）+ link-local（169.254/16）。
// 部署变更（公网代理、非标准内网）可通过 TrustedProxies 扩展。
package clientip

import (
	"net"
	"net/http"
	"strings"
	"sync"
)

// 默认 trusted proxy CIDR（loopback + 私网 + link-local）。
// 公开公网 IP 不在白名单 — 如果用户把 Go 服务直接暴露到公网（应该不会），
// X-Forwarded-For 会被忽略，回落到 r.RemoteAddr（攻击者的真实 IP）。
var defaultTrustedCIDRs = []string{
	"127.0.0.0/8",    // IPv4 loopback
	"::1/128",        // IPv6 loopback
	"10.0.0.0/8",     // RFC1918
	"172.16.0.0/12",  // RFC1918
	"192.168.0.0/16", // RFC1918
	"169.254.0.0/16", // IPv4 link-local
	"fc00::/7",       // IPv6 unique local
}

var (
	mu             sync.RWMutex
	trustedCIDRs   = append([]*net.IPNet(nil), parseCIDRs(defaultTrustedCIDRs)...)
	trustedRawNets []*net.IPNet
)

// SetTrustedProxies 覆盖默认 trusted proxy CIDR 列表（main.go 启动时调）。
// 不传 = 用默认；传空 = 完全不信任 proxy header（用 r.RemoteAddr）。
func SetTrustedProxies(cidrs []string) {
	mu.Lock()
	defer mu.Unlock()
	trustedCIDRs = parseCIDRs(cidrs)
	trustedRawNets = nil
}

// AppendTrustedProxy 加一条 CIDR（运行时扩展用）。
func AppendTrustedProxy(cidr string) error {
	_, n, err := parseCIDR(cidr)
	if err != nil {
		return err
	}
	mu.Lock()
	trustedCIDRs = append(trustedCIDRs, n)
	mu.Unlock()
	return nil
}

func parseCIDRs(in []string) []*net.IPNet {
	out := make([]*net.IPNet, 0, len(in))
	for _, s := range in {
		_, n, err := parseCIDR(s)
		if err != nil {
			continue
		}
		out = append(out, n)
	}
	return out
}

func parseCIDR(s string) (net.IP, *net.IPNet, error) {
	_, n, err := net.ParseCIDR(s)
	if err != nil {
		return nil, nil, err
	}
	return n.IP, n, nil
}

// RemoteHost 拿 r.RemoteAddr 的 host 部分（去掉端口）。
func RemoteHost(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// IsTrustedProxy 判断 r.RemoteAddr 是否在 trusted CIDR 列表中。
// 命中后才信任 X-Forwarded-For 等 proxy header。
func IsTrustedProxy(r *http.Request) bool {
	host := RemoteHost(r)
	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}
	mu.RLock()
	defer mu.RUnlock()
	for _, n := range trustedCIDRs {
		if n.Contains(ip) {
			return true
		}
	}
	return false
}

// Real 拿真实客户端 IP：
//   - 如果 r.RemoteAddr 在 trusted proxy → 信任 X-Forwarded-For（取第一个 IP）
//   - 否则 → 忽略 proxy headers，用 r.RemoteAddr
//
// 优先级：X-Forwarded-For 第一段 > X-Real-IP > r.RemoteAddr。
func Real(r *http.Request) string {
	if !IsTrustedProxy(r) {
		return RemoteHost(r)
	}
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		// 多个 IP 时取第一个（最近的客户端；proxy 自己 append 在最后）
		if i := strings.Index(xff, ","); i >= 0 {
			return strings.TrimSpace(xff[:i])
		}
		return strings.TrimSpace(xff)
	}
	if xri := r.Header.Get("X-Real-IP"); xri != "" {
		return strings.TrimSpace(xri)
	}
	return RemoteHost(r)
}
