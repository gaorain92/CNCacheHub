package access

import (
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
)

// === unit tests for helpers ===

func TestParseCIDRList_Empty(t *testing.T) {
	got := ParseCIDRList("")
	if got != nil {
		t.Fatalf("empty input should return nil, got %v", got)
	}
}

func TestParseCIDRList_Valid(t *testing.T) {
	got := ParseCIDRList("10.0.0.0/8, 192.168.0.0/16 , 172.16.0.0/12")
	want := []string{"10.0.0.0/8", "192.168.0.0/16", "172.16.0.0/12"}
	if len(got) != len(want) {
		t.Fatalf("len = %d, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("[%d] got %q want %q", i, got[i], want[i])
		}
	}
}

func TestParseCIDRList_InvalidSkipped(t *testing.T) {
	got := ParseCIDRList("10.0.0.0/8,not-a-cidr,192.168.0.0/16,")
	if len(got) != 2 {
		t.Fatalf("should skip invalid + empty, got %v", got)
	}
}

func TestIsLoopback(t *testing.T) {
	cases := []struct {
		ip   string
		want bool
	}{
		{"127.0.0.1", true},
		{"127.255.255.255", true},
		{"::1", true},
		{"10.0.0.1", false},
		{"8.8.8.8", false},
		{"not-an-ip", false},
	}
	for _, c := range cases {
		if got := isLoopback(c.ip); got != c.want {
			t.Errorf("isLoopback(%q) = %v, want %v", c.ip, got, c.want)
		}
	}
}

func TestIpMatchesCIDRs(t *testing.T) {
	cidrs := []string{"10.0.0.0/8", "192.168.0.0/16"}
	cases := []struct {
		ip   string
		want bool
	}{
		{"10.1.2.3", true},
		{"10.255.255.255", true},
		{"192.168.0.1", true},
		{"192.168.255.255", true},
		{"11.0.0.1", false},
		{"172.16.0.1", false},
		{"8.8.8.8", false},
	}
	for _, c := range cases {
		ip := net.ParseIP(c.ip)
		if ip == nil {
			t.Errorf("parse %q failed", c.ip)
			continue
		}
		if got := ipMatchesCIDRs(ip, cidrs); got != c.want {
			t.Errorf("ipMatchesCIDRs(%q) = %v, want %v", c.ip, got, c.want)
		}
	}
}

func TestConstantTimeEqual(t *testing.T) {
	if !constantTimeEqual("abc", "abc") {
		t.Error("equal should return true")
	}
	if constantTimeEqual("abc", "abd") {
		t.Error("different should return false")
	}
	if constantTimeEqual("abc", "abcd") {
		t.Error("different length should return false")
	}
}

func TestExtractToken(t *testing.T) {
	cases := []struct {
		name   string
		header http.Header
		want   string
	}{
		{"X-CNCacheHub-Token", http.Header{"X-Cncachehub-Token": []string{"secret"}}, "secret"},
		{"Authorization Bearer", http.Header{"Authorization": []string{"Bearer mytoken"}}, "mytoken"},
		{"Authorization basic (no Bearer)", http.Header{"Authorization": []string{"Basic xxx"}}, ""},
		{"no header", http.Header{}, ""},
		{"X overrides Auth", http.Header{
			"X-Cncachehub-Token": []string{"custom"},
			"Authorization":      []string{"Bearer other"},
		}, "custom"},
	}
	for _, c := range cases {
		r := &http.Request{Header: c.header}
		if got := extractToken(r); got != c.want {
			t.Errorf("%s: got %q want %q", c.name, got, c.want)
		}
	}
}

func TestRealClientIP(t *testing.T) {
	cases := []struct {
		name       string
		xff        string
		xri        string
		remoteAddr string
		want       string
	}{
		{"XFF first", "1.2.3.4, 10.0.0.1", "", "127.0.0.1:5555", "1.2.3.4"},
		{"XRI second", "", "5.6.7.8", "127.0.0.1:5555", "5.6.7.8"},
		{"fallback to RemoteAddr", "", "", "127.0.0.1:5555", "127.0.0.1"},
		{"RemoteAddr without port", "", "", "127.0.0.1", "127.0.0.1"},
	}
	for _, c := range cases {
		r := &http.Request{
			Header:     http.Header{},
			RemoteAddr: c.remoteAddr,
		}
		if c.xff != "" {
			r.Header.Set("X-Forwarded-For", c.xff)
		}
		if c.xri != "" {
			r.Header.Set("X-Real-IP", c.xri)
		}
		if got := realClientIP(r); got != c.want {
			t.Errorf("%s: got %q want %q", c.name, got, c.want)
		}
	}
}

// === middleware tests ===

func TestMiddleware_DisabledPasses(t *testing.T) {
	resolve := StaticResolver(Config{Enabled: false})
	called := false
	h := Middleware(resolve)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(200)
	}))

	r := httptest.NewRequest("GET", "/v2/", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, r)
	if !called {
		t.Error("handler not called when disabled")
	}
	if rr.Code != 200 {
		t.Errorf("code = %d, want 200", rr.Code)
	}
}

func TestMiddleware_NoMatch401(t *testing.T) {
	resolve := StaticResolver(Config{
		Enabled:        true,
		Token:          "secret123",
		IPWhitelist:    []string{"10.0.0.0/8"},
		LoopbackBypass: false,
	})
	called := false
	h := Middleware(resolve)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
	}))

	r := httptest.NewRequest("GET", "/v2/", nil)
	r.RemoteAddr = "8.8.8.8:1234" // 外部 IP, 不在白名单
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, r)
	if called {
		t.Error("handler should not be called")
	}
	if rr.Code != 401 {
		t.Errorf("code = %d, want 401", rr.Code)
	}
}

func TestMiddleware_TokenPasses(t *testing.T) {
	resolve := StaticResolver(Config{
		Enabled: true,
		Token:   "secret123",
	})
	called := false
	h := Middleware(resolve)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
	}))

	r := httptest.NewRequest("GET", "/v2/", nil)
	r.RemoteAddr = "8.8.8.8:1234"
	r.Header.Set("X-CNCacheHub-Token", "secret123")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, r)
	if !called {
		t.Error("token match should pass")
	}
	if rr.Code != 200 {
		t.Errorf("code = %d, want 200", rr.Code)
	}
}

func TestMiddleware_BearerTokenPasses(t *testing.T) {
	resolve := StaticResolver(Config{Enabled: true, Token: "secret123"})
	called := false
	h := Middleware(resolve)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
	}))

	r := httptest.NewRequest("GET", "/v2/", nil)
	r.RemoteAddr = "8.8.8.8:1234"
	r.Header.Set("Authorization", "Bearer secret123")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, r)
	if !called {
		t.Error("Bearer token should pass")
	}
}

func TestMiddleware_IPWhitelistPasses(t *testing.T) {
	resolve := StaticResolver(Config{
		Enabled:     true,
		IPWhitelist: []string{"10.0.0.0/8", "192.168.0.0/16"},
	})
	called := false
	h := Middleware(resolve)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
	}))

	r := httptest.NewRequest("GET", "/v2/", nil)
	r.RemoteAddr = "10.5.5.5:1234"
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, r)
	if !called {
		t.Error("IP in whitelist should pass")
	}
}

func TestMiddleware_LoopbackBypassPasses(t *testing.T) {
	resolve := StaticResolver(Config{
		Enabled:        true,
		LoopbackBypass: true,
		// 没 token 没 IP 白名单
	})
	called := false
	h := Middleware(resolve)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
	}))

	r := httptest.NewRequest("GET", "/v2/", nil)
	r.RemoteAddr = "127.0.0.1:5555"
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, r)
	if !called {
		t.Error("loopback should pass when LoopbackBypass=true")
	}
}

func TestMiddleware_LoopbackBypassDisabledRejects(t *testing.T) {
	resolve := StaticResolver(Config{
		Enabled:        true,
		LoopbackBypass: false,
	})
	called := false
	h := Middleware(resolve)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
	}))

	r := httptest.NewRequest("GET", "/v2/", nil)
	r.RemoteAddr = "127.0.0.1:5555"
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, r)
	if called {
		t.Error("loopback should NOT pass when LoopbackBypass=false and no other creds")
	}
	if rr.Code != 401 {
		t.Errorf("code = %d, want 401", rr.Code)
	}
}

func TestMiddleware_EmptyConfigStillRejects(t *testing.T) {
	// enabled=true 但 token="" + whitelist=[] + loopback=false → 全部拒绝
	resolve := StaticResolver(Config{Enabled: true, LoopbackBypass: false})
	h := Middleware(resolve)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("handler should not be called")
	}))

	r := httptest.NewRequest("GET", "/v2/", nil)
	r.RemoteAddr = "8.8.8.8:1234"
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, r)
	if rr.Code != 401 {
		t.Errorf("code = %d, want 401", rr.Code)
	}
}

func TestMutableResolver_Reload(t *testing.T) {
	get, set := MutableResolver(Config{Enabled: false})
	if get().Enabled {
		t.Error("initial should be disabled")
	}
	set(Config{Enabled: true, Token: "x"})
	if !get().Enabled {
		t.Error("after set should be enabled")
	}
	if get().Token != "x" {
		t.Error("token not updated")
	}
}

func TestConfig_String_NoLeakToken(t *testing.T) {
	c := Config{Enabled: true, Token: "supersecret", IPWhitelist: []string{"10.0.0.0/8"}}
	s := c.String()
	if contains(s, "supersecret") {
		t.Errorf("String() leaked token: %s", s)
	}
	if !contains(s, "***") {
		t.Errorf("String() should mask token: %s", s)
	}
}

func contains(haystack, needle string) bool {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}
