package clientip

import (
	"net/http"
	"testing"
)

func TestReal_TrustedProxyUsesXForwardedFor(t *testing.T) {
	// loopback + X-Forwarded-For → 信任 header
	r := &http.Request{
		Header:     http.Header{},
		RemoteAddr: "127.0.0.1:1234",
	}
	r.Header.Set("X-Forwarded-For", "203.0.113.5, 10.0.0.1")
	if got := Real(r); got != "203.0.113.5" {
		t.Errorf("Real = %q, want 203.0.113.5 (first X-Forwarded-For hop)", got)
	}
}

func TestReal_TrustedProxyUsesXRealIP(t *testing.T) {
	r := &http.Request{
		Header:     http.Header{},
		RemoteAddr: "10.0.0.5:80",
	}
	r.Header.Set("X-Real-IP", "198.51.100.7")
	if got := Real(r); got != "198.51.100.7" {
		t.Errorf("Real = %q, want 198.51.100.7 (X-Real-IP)", got)
	}
}

func TestReal_UntrustedProxyFallsBackToRemoteAddr(t *testing.T) {
	// 公网直连（不在 trusted CIDR）+ X-Forwarded-For 伪造 → 忽略
	r := &http.Request{
		Header:     http.Header{},
		RemoteAddr: "203.0.113.99:5555", // 公网 IP，不在 trusted
	}
	r.Header.Set("X-Forwarded-For", "1.2.3.4")
	if got := Real(r); got != "203.0.113.99" {
		t.Errorf("Real = %q, want 203.0.113.99 (RemoteAddr; XFF ignored)", got)
	}
}

func TestReal_TrustedProxyNoHeadersUsesRemoteAddr(t *testing.T) {
	r := &http.Request{
		Header:     http.Header{},
		RemoteAddr: "127.0.0.1:8080",
	}
	if got := Real(r); got != "127.0.0.1" {
		t.Errorf("Real = %q, want 127.0.0.1", got)
	}
}

func TestIsTrustedProxy(t *testing.T) {
	cases := []struct {
		remote string
		want   bool
	}{
		{"127.0.0.1:80", true},
		{"127.0.0.53:53", true},
		{"10.1.2.3:5000", true},
		{"172.16.0.1:443", true},
		{"172.31.255.255:443", true},
		{"172.32.0.1:443", false}, // 超出 172.16/12
		{"192.168.1.1:80", true},
		{"169.254.1.1:80", true},
		{"[::1]:80", true},
		{"[fc00::1]:80", true},
		{"203.0.113.1:80", false}, // 公网
		{"198.51.100.1:80", false},
		{"8.8.8.8:53", false},
	}
	for _, c := range cases {
		t.Run(c.remote, func(t *testing.T) {
			r := &http.Request{Header: http.Header{}, RemoteAddr: c.remote}
			if got := IsTrustedProxy(r); got != c.want {
				t.Errorf("IsTrustedProxy(%q) = %v, want %v", c.remote, got, c.want)
			}
		})
	}
}

func TestSetTrustedProxies_Override(t *testing.T) {
	// 加一条公网 CIDR
	if err := AppendTrustedProxy("203.0.113.0/24"); err != nil {
		t.Fatalf("AppendTrustedProxy: %v", err)
	}
	// 验证这条 CIDR 里的 IP 现在被信任
	r := &http.Request{Header: http.Header{}, RemoteAddr: "203.0.113.42:5555"}
	r.Header.Set("X-Forwarded-For", "8.8.8.8")
	if got := Real(r); got != "8.8.8.8" {
		t.Errorf("after AppendTrustedProxy(203.0.113.0/24), Real = %q, want 8.8.8.8", got)
	}

	// 还原（避免污染其他 test）
	t.Cleanup(func() {
		SetTrustedProxies(defaultTrustedCIDRs)
	})
}

func TestSetTrustedProxies_Empty(t *testing.T) {
	SetTrustedProxies(nil)
	r := &http.Request{Header: http.Header{}, RemoteAddr: "127.0.0.1:1234"}
	r.Header.Set("X-Forwarded-For", "1.2.3.4")
	// 空 trusted → 全部不信任，回落 RemoteAddr
	if got := Real(r); got != "127.0.0.1" {
		t.Errorf("Real with empty trusted = %q, want 127.0.0.1", got)
	}
	t.Cleanup(func() { SetTrustedProxies(defaultTrustedCIDRs) })
}
