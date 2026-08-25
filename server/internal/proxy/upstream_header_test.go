// Tests for copyRequestHeaders: proxy header filtering.
package proxy

import (
	"net/http"
	"testing"
)

func TestCopyRequestHeaders_FiltersProxyHeaders(t *testing.T) {
	// 用户恶意设代理头，验证被剥掉。
	src := http.Header{}
	src.Set("X-Forwarded-For", "1.2.3.4")
	src.Set("X-Real-IP", "5.6.7.8")
	src.Set("X-Forwarded-Proto", "https")
	src.Set("X-Forwarded-Host", "evil.com")
	src.Set("X-Forwarded-Port", "443")
	src.Set("X-Forwarded-Scheme", "https")
	src.Set("Forwarded", "for=1.2.3.4")
	src.Set("Client-Ip", "9.9.9.9")
	src.Set("Cf-Connecting-Ip", "8.8.8.8")
	src.Set("True-Client-Ip", "7.7.7.7")
	src.Set("Fastly-Client-Ip", "6.6.6.6")
	src.Set("X-Client-Ip", "5.5.5.5")
	src.Set("X-Original-Forwarded-For", "4.4.4.4")
	src.Set("Host", "evil.example.com")
	src.Set("Authorization", "Bearer leaked")
	src.Set("Cookie", "session=secret")
	// 正常业务头应该透传
	src.Set("User-Agent", "curl/8.0")
	src.Set("Accept", "application/json")
	src.Set("X-Custom-Header", "kept")

	dst := http.Header{}
	copyRequestHeaders(dst, src)

	// 1. 代理头应该被剥掉
	bad := []string{
		"X-Forwarded-For", "X-Real-Ip", "X-Real-IP",
		"X-Forwarded-Proto", "X-Forwarded-Host",
		"X-Forwarded-Port", "X-Forwarded-Scheme",
		"Forwarded", "Client-Ip",
		"Cf-Connecting-Ip", "True-Client-Ip", "Fastly-Client-Ip",
		"X-Client-Ip", "X-Original-Forwarded-For",
		"Host", "Authorization", "Cookie",
	}
	for _, h := range bad {
		if v := dst.Get(h); v != "" {
			t.Errorf("proxy header %q leaked: %q", h, v)
		}
	}

	// 2. 业务头保留
	if got := dst.Get("User-Agent"); got != "curl/8.0" {
		t.Errorf("User-Agent = %q, want curl/8.0", got)
	}
	if got := dst.Get("Accept"); got != "application/json" {
		t.Errorf("Accept = %q, want application/json", got)
	}
	if got := dst.Get("X-Custom-Header"); got != "kept" {
		t.Errorf("X-Custom-Header = %q, want kept", got)
	}
}

func TestCopyRequestHeaders_SkipsInternalDebug(t *testing.T) {
	src := http.Header{}
	src.Set("X-CNCH-Request-Id", "internal-trace")
	src.Set("X-Cnch-Region", "cn-bj")
	src.Set("X-Cnch-Version", "v0.1.0")

	dst := http.Header{}
	copyRequestHeaders(dst, src)

	for k := range src {
		if len(k) >= 5 && (k[:5] == "X-CNC" || (len(k) >= 6 && k[:6] == "X-Cnch")) {
			if dst.Get(k) != "" {
				t.Errorf("internal header %q leaked", k)
			}
		}
	}
}
