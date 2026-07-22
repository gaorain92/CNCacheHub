package dnsserver

import (
	"strings"
	"testing"
)

func TestMatchDomain(t *testing.T) {
	cfg := Config{
		DomainRules: []string{
			"*.steamcontent.com",
			"client-download.steampowered.com",
			"*.steamstatic.com",
		},
	}
	s := &Server{cfg: cfg}

	cases := []struct {
		domain string
		want   bool
	}{
		{"cdn.steamcontent.com", true},
		{"client-download.steampowered.com", true},
		{"avatars.steamstatic.com", true},
		{"example.com", false},
		{"steamcontent.com", false}, // 通配符不会匹配裸 apex
		{"foo.client-download.steampowered.com", false}, // 不是 *. 规则
		{"client-download.steampowered.com.", true},    // 末尾带点也能匹配
	}
	for _, c := range cases {
		got := s.matchDomain(c.domain)
		if got != c.want {
			t.Errorf("matchDomain(%q) = %v, want %v", c.domain, got, c.want)
		}
	}
}

func TestMatchDomain_EmptyRules(t *testing.T) {
	s := &Server{cfg: Config{}}
	if s.matchDomain("anything.com") {
		t.Error("empty rules should never match")
	}
}

func TestDefaultConfig(t *testing.T) {
	c := DefaultConfig()
	if c.ListenAddr == "" {
		t.Error("default ListenAddr empty")
	}
	if c.Upstream == "" {
		t.Error("default Upstream empty")
	}
	if c.AnswerIP == "" {
		t.Error("default AnswerIP empty")
	}
	if len(c.DomainRules) < 5 {
		t.Errorf("default DomainRules = %d, want >= 5", len(c.DomainRules))
	}
	// 必须包含 LANCache 关键规则
	wantRule := "*.steamcontent.com"
	found := false
	for _, r := range c.DomainRules {
		if strings.TrimSpace(r) == wantRule {
			found = true
		}
	}
	if !found {
		t.Errorf("default DomainRules missing %q", wantRule)
	}
}

func TestStats_Initial(t *testing.T) {
	s := NewServer(DefaultConfig(), nil)
	stats := s.Stats()
	if stats.TotalQueries != 0 || stats.HitQueries != 0 || stats.MissQueries != 0 {
		t.Errorf("initial stats should be zero, got %+v", stats)
	}
}

func TestServer_StartDisabled_NoOp(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Enabled = false
	s := NewServer(cfg, nil)
	// 不能在测试里真启端口，但 cfg.Enabled=false 时 Start 必须 no-op
	if err := s.Start(nil); err != nil {
		t.Errorf("disabled server Start should not error, got %v", err)
	}
	if s.udpSrv != nil {
		t.Error("disabled server should not have udp listener")
	}
}

func TestServer_StartInvalidAddr_Errors(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Enabled = true
	cfg.ListenAddr = "" // 故意留空
	s := NewServer(cfg, nil)
	if err := s.Start(nil); err == nil {
		t.Error("empty ListenAddr should error")
	}
}
