package dnsserver

import (
	"context"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/miekg/dns"
)

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

// dnsTestPortCounter 分配 (listen, upstream) 端口对，每对间隔 10 避免冲突。
var dnsTestPortCounter atomic.Int32

// allocTestPorts 返回一对未被占用的端口。
// 返回 (listen, upstream) — caller 负责分别绑定。
func allocTestPorts() (int, int) {
	n := int(dnsTestPortCounter.Add(1))
	base := 48000 + n*10
	return base, base + 1
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	neg := n < 0
	if neg {
		n = -n
	}
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

// newTestConfig 构造一个 enabled=true、用指定端口的 config。
func newTestConfig(listen, upstream int) Config {
	return Config{
		Enabled:     true,
		ListenAddr:  "127.0.0.1:" + itoa(listen),
		Upstream:    "127.0.0.1:" + itoa(upstream),
		AnswerIP:    "127.0.0.1",
		DomainRules: []string{"*.example.com", "exact.test"},
	}
}

// startTestUpstream 起一个 miekg/dns server 处理 query。
// 返回 cleanup 函数。
func startTestUpstream(t *testing.T, port int, respHandler dns.HandlerFunc) func() {
	t.Helper()
	mux := dns.NewServeMux()
	mux.HandleFunc(".", respHandler)
	srv := &dns.Server{Addr: "127.0.0.1:" + itoa(port), Net: "udp", Handler: mux}
	started := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		close(started)
		done <- srv.ListenAndServe()
	}()
	<-started
	time.Sleep(50 * time.Millisecond)
	return func() {
		_ = srv.Shutdown()
		<-done
	}
}

// queryLocal 发 DNS query 到 addr，返回 response。
func queryLocal(t *testing.T, addr, name string, qtype uint16) *dns.Msg {
	t.Helper()
	c := &dns.Client{Net: "udp", Timeout: 2 * time.Second}
	m := new(dns.Msg)
	m.SetQuestion(dns.Fqdn(name), qtype)
	resp, _, err := c.Exchange(m, addr)
	if err != nil {
		t.Fatalf("dns query %s: %v", name, err)
	}
	return resp
}

// mockResponseWriter — 最小化的 dns.ResponseWriter 实现，用于直接测 handleQuery。
type mockResponseWriter struct {
	msg     *dns.Msg
	writeErr error
}

func (m *mockResponseWriter) LocalAddr() net.Addr {
	return &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)}
}
func (m *mockResponseWriter) RemoteAddr() net.Addr {
	return &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 12345}
}
func (m *mockResponseWriter) WriteMsg(msg *dns.Msg) error {
	m.msg = msg
	return m.writeErr
}
func (m *mockResponseWriter) Write(b []byte) (int, error) {
	return len(b), m.writeErr
}
func (m *mockResponseWriter) Close() error         { return nil }
func (m *mockResponseWriter) TsigStatus() error     { return nil }
func (m *mockResponseWriter) TsigTimersOnly(bool)   {}
func (m *mockResponseWriter) Hijack()               {}

// ---------------------------------------------------------------------------
// MatchDomain
// ---------------------------------------------------------------------------

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
		{"steamcontent.com", false},                     // 通配符不会匹配裸 apex
		{"foo.client-download.steampowered.com", false}, // 不是 *. 规则
		{"client-download.steampowered.com.", true},     // 末尾带点也能匹配
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

func TestMatchDomain_CaseInsensitive(t *testing.T) {
	s := &Server{cfg: Config{DomainRules: []string{"*.Example.COM"}}}
	if !s.matchDomain("foo.example.com") {
		t.Error("lowercase query should match uppercase rule")
	}
	if !s.matchDomain("FOO.EXAMPLE.COM") {
		t.Error("uppercase query should match uppercase rule")
	}
}

func TestMatchDomain_EmptyQuery(t *testing.T) {
	s := &Server{cfg: Config{DomainRules: []string{"*.example.com"}}}
	if s.matchDomain("") {
		t.Error("empty query should not match")
	}
	if s.matchDomain("   ") {
		t.Error("whitespace query should not match")
	}
}

func TestMatchDomain_SkipEmptyRules(t *testing.T) {
	s := &Server{cfg: Config{DomainRules: []string{"", "  ", "*.example.com"}}}
	if !s.matchDomain("foo.example.com") {
		t.Error("empty rules should be skipped, real rule should match")
	}
}

func TestMatchDomain_TrailingDot(t *testing.T) {
	s := &Server{cfg: Config{DomainRules: []string{"exact.test"}}}
	if !s.matchDomain("exact.test.") {
		t.Error("trailing dot should be normalized")
	}
	if !s.matchDomain("exact.test") {
		t.Error("no trailing dot should also match")
	}
}

// ---------------------------------------------------------------------------
// DefaultConfig + Stats initial
// ---------------------------------------------------------------------------

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

// ---------------------------------------------------------------------------
// Start / Stop / Reload
// ---------------------------------------------------------------------------

func TestStart_Disabled_NoOp(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Enabled = false
	s := NewServer(cfg, nil)
	if err := s.Start(context.Background()); err != nil {
		t.Fatalf("Start(disabled) should be no-op, got: %v", err)
	}
	if s.udpSrv != nil {
		t.Error("udpSrv should remain nil when disabled")
	}
}

func TestStart_MissingListenAddr_Errors(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Enabled = true
	cfg.ListenAddr = ""
	s := NewServer(cfg, nil)
	if err := s.Start(context.Background()); err == nil {
		t.Error("empty ListenAddr should error")
	}
}

func TestStart_MissingAnswerIP_Errors(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Enabled = true
	listen, upstream := allocTestPorts()
	cfg.ListenAddr = "127.0.0.1:" + itoa(listen)
	cfg.Upstream = "127.0.0.1:" + itoa(upstream)
	cfg.AnswerIP = ""
	if err := NewServer(cfg, nil).Start(context.Background()); err == nil {
		t.Error("empty AnswerIP should error")
	}
}

func TestStart_MissingUpstream_Errors(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Enabled = true
	listen, _ := allocTestPorts()
	cfg.ListenAddr = "127.0.0.1:" + itoa(listen)
	cfg.Upstream = ""
	if err := NewServer(cfg, nil).Start(context.Background()); err == nil {
		t.Error("empty Upstream should error")
	}
}

func TestStartStop_HappyPath(t *testing.T) {
	listen, upstream := allocTestPorts()
	cfg := newTestConfig(listen, upstream)
	cleanup := startTestUpstream(t, upstream, func(w dns.ResponseWriter, r *dns.Msg) {
		w.WriteMsg(&dns.Msg{})
	})
	defer cleanup()

	s := NewServer(cfg, nil)
	if err := s.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if s.udpSrv == nil || s.tcpSrv == nil {
		t.Fatal("expected udpSrv+tcpSrv after Start")
	}
	time.Sleep(100 * time.Millisecond)
	if err := s.Stop(); err != nil {
		t.Errorf("Stop: %v", err)
	}
	if s.udpSrv != nil || s.tcpSrv != nil {
		t.Error("udpSrv/tcpSrv should be nil after Stop")
	}
}

func TestStart_Idempotent(t *testing.T) {
	listen, upstream := allocTestPorts()
	cfg := newTestConfig(listen, upstream)
	cleanup := startTestUpstream(t, upstream, func(w dns.ResponseWriter, r *dns.Msg) {
		w.WriteMsg(&dns.Msg{})
	})
	defer cleanup()

	s := NewServer(cfg, nil)
	if err := s.Start(context.Background()); err != nil {
		t.Fatalf("first Start: %v", err)
	}
	if err := s.Start(context.Background()); err != nil {
		t.Errorf("second Start should be no-op, got: %v", err)
	}
	_ = s.Stop()
}

func TestStop_WithoutStart_NoError(t *testing.T) {
	s := NewServer(DefaultConfig(), nil)
	if err := s.Stop(); err != nil {
		t.Errorf("Stop without Start should be no-op, got: %v", err)
	}
}

func TestStop_MultipleSafe(t *testing.T) {
	listen, upstream := allocTestPorts()
	cfg := newTestConfig(listen, upstream)
	cleanup := startTestUpstream(t, upstream, func(w dns.ResponseWriter, r *dns.Msg) {
		w.WriteMsg(&dns.Msg{})
	})
	defer cleanup()

	s := NewServer(cfg, nil)
	_ = s.Start(context.Background())
	time.Sleep(50 * time.Millisecond)
	if err := s.Stop(); err != nil {
		t.Errorf("first Stop: %v", err)
	}
	if err := s.Stop(); err != nil {
		t.Errorf("second Stop should be no-op, got: %v", err)
	}
}

func TestReload_ReplacesConfig(t *testing.T) {
	listen, upstream := allocTestPorts()
	cfg := newTestConfig(listen, upstream)
	cleanup := startTestUpstream(t, upstream, func(w dns.ResponseWriter, r *dns.Msg) {
		w.WriteMsg(&dns.Msg{})
	})
	defer cleanup()

	s := NewServer(cfg, nil)
	if err := s.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer s.Stop()

	// 新 config 用一对完全不同的端口（间隔 10 防止和当前撞）
	newListen, newUpstream := allocTestPorts()
	// 跳过分配给当前 test 的同一对
	if newListen == listen {
		newListen, newUpstream = allocTestPorts()
	}
	newCfg := newTestConfig(newListen, newUpstream)
	newCfg.DomainRules = []string{"*.newdomain.com"}
	newCleanup := startTestUpstream(t, newUpstream, func(w dns.ResponseWriter, r *dns.Msg) {
		w.WriteMsg(&dns.Msg{})
	})
	defer newCleanup()

	if err := s.Reload(context.Background(), newCfg); err != nil {
		t.Fatalf("Reload: %v", err)
	}
	got := s.Config()
	if got.DomainRules[0] != "*.newdomain.com" {
		t.Errorf("DomainRules not updated: %v", got.DomainRules)
	}
	if got.ListenAddr != newCfg.ListenAddr {
		t.Errorf("ListenAddr = %q, want %q", got.ListenAddr, newCfg.ListenAddr)
	}
	if s.udpSrv == nil {
		t.Error("udpSrv should be re-created after Reload")
	}
}

// ---------------------------------------------------------------------------
// 端口冲突
// ---------------------------------------------------------------------------

func TestStart_PortInUse_Errors(t *testing.T) {
	listen, upstream := allocTestPorts()
	// UDP 占用端口（DNS 用 UDP）
	occupy, err := net.ListenPacket("udp", "127.0.0.1:"+itoa(listen))
	if err != nil {
		t.Fatalf("occupy udp port: %v", err)
	}
	defer occupy.Close()

	cfg := newTestConfig(listen, upstream)
	s := NewServer(cfg, nil)
	err = s.Start(context.Background())
	if err == nil {
		_ = s.Stop()
		t.Error("Start on occupied UDP port should error")
	}
	if s.udpSrv != nil {
		t.Error("udpSrv should be cleared after failed Start")
	}
}

// ---------------------------------------------------------------------------
// handleQuery 集成
// ---------------------------------------------------------------------------

func TestHandleQuery_WhitelistHit_ReturnsLocalA(t *testing.T) {
	listen, upstream := allocTestPorts()
	cfg := newTestConfig(listen, upstream)
	cfg.AnswerIP = "10.0.0.99"
	cfg.DomainRules = []string{"*.example.com"}
	cleanup := startTestUpstream(t, upstream, func(w dns.ResponseWriter, r *dns.Msg) {
		t.Error("upstream should NOT be called for whitelisted domain")
		w.WriteMsg(&dns.Msg{})
	})
	defer cleanup()

	s := NewServer(cfg, nil)
	if err := s.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer s.Stop()
	time.Sleep(100 * time.Millisecond)

	resp := queryLocal(t, cfg.ListenAddr, "foo.example.com", dns.TypeA)
	if len(resp.Answer) != 1 {
		t.Fatalf("expected 1 A record, got %d", len(resp.Answer))
	}
	a, ok := resp.Answer[0].(*dns.A)
	if !ok {
		t.Fatalf("answer not *dns.A: %T", resp.Answer[0])
	}
	if a.A.String() != "10.0.0.99" {
		t.Errorf("A = %s, want 10.0.0.99", a.A.String())
	}
	if resp.Rcode != dns.RcodeSuccess {
		t.Errorf("Rcode = %d, want success", resp.Rcode)
	}
	if s.Stats().HitQueries != 1 {
		t.Errorf("HitQueries = %d, want 1", s.Stats().HitQueries)
	}
}

func TestHandleQuery_Miss_ForwardsToUpstream(t *testing.T) {
	listen, upstream := allocTestPorts()
	cfg := newTestConfig(listen, upstream)
	cfg.DomainRules = []string{"*.example.com"}

	// 用 atomic.Int32 防 race：upstream handler 在自己 goroutine 写 hits，
	// 主测试 goroutine 读 hits — `-race` 模式下不加同步会报 DATA RACE。
	var hits atomic.Int32
	cleanup := startTestUpstream(t, upstream, func(w dns.ResponseWriter, r *dns.Msg) {
		hits.Add(1)
		resp := new(dns.Msg)
		resp.SetReply(r)
		resp.Answer = []dns.RR{&dns.A{
			Hdr: dns.RR_Header{Name: r.Question[0].Name, Rrtype: dns.TypeA, Class: dns.ClassINET, Ttl: 60},
			A:   net.ParseIP("1.2.3.4").To4(),
		}}
		_ = w.WriteMsg(resp)
	})
	defer cleanup()

	s := NewServer(cfg, nil)
	if err := s.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer s.Stop()
	time.Sleep(100 * time.Millisecond)

	resp := queryLocal(t, cfg.ListenAddr, "github.com", dns.TypeA)
	if got := hits.Load(); got != 1 {
		t.Errorf("upstream called %d times, want 1", got)
	}
	if len(resp.Answer) != 1 {
		t.Fatalf("expected 1 A record from upstream, got %d", len(resp.Answer))
	}
	a := resp.Answer[0].(*dns.A)
	if a.A.String() != "1.2.3.4" {
		t.Errorf("upstream A = %s, want 1.2.3.4", a.A.String())
	}
	if s.Stats().MissQueries != 1 {
		t.Errorf("MissQueries = %d, want 1", s.Stats().MissQueries)
	}
}

func TestHandleQuery_AAAA_ForwardsToUpstream(t *testing.T) {
	listen, upstream := allocTestPorts()
	cfg := newTestConfig(listen, upstream)
	cfg.DomainRules = []string{"*.example.com"} // A 记录会命中

	// 用 atomic.Int32 防 race：upstream handler 在自己 goroutine 写 hits，
	// 主测试 goroutine 读 hits — `-race` 模式下不加同步会报 DATA RACE。
	var hits atomic.Int32
	cleanup := startTestUpstream(t, upstream, func(w dns.ResponseWriter, r *dns.Msg) {
		hits.Add(1)
		// 必须 SetReply — 客户端靠 Id 匹配 request/response
		resp := new(dns.Msg).SetReply(r)
		_ = w.WriteMsg(resp)
	})
	defer cleanup()

	s := NewServer(cfg, nil)
	if err := s.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer s.Stop()
	time.Sleep(100 * time.Millisecond)

	// AAAA 即使匹配白名单也走 upstream（白名单只对 A 生效）
	_ = queryLocal(t, cfg.ListenAddr, "foo.example.com", dns.TypeAAAA)
	if got := hits.Load(); got != 1 {
		t.Errorf("upstream should be called for AAAA, hits = %d", got)
	}
}

func TestHandleQuery_EmptyQuestion_Refused(t *testing.T) {
	// 直接调 handleQuery（Exchange 客户端对 0-question response 会自己返 FORMERR，
	// 无法测真实 server 行为）
	s := &Server{cfg: Config{AnswerIP: "127.0.0.1"}}
	w := &mockResponseWriter{}
	req := new(dns.Msg) // 0 questions
	s.handleQuery(w, req)
	if w.msg == nil {
		t.Fatal("expected response, got nil")
	}
	if w.msg.Rcode != dns.RcodeRefused {
		t.Errorf("Rcode = %d, want Refused", w.msg.Rcode)
	}
}

func TestHandleQuery_InvalidAnswerIP_Refused(t *testing.T) {
	listen, upstream := allocTestPorts()
	cfg := newTestConfig(listen, upstream)
	cfg.AnswerIP = "not-an-ip"
	cfg.DomainRules = []string{"*.example.com"}
	cleanup := startTestUpstream(t, upstream, func(w dns.ResponseWriter, r *dns.Msg) {})
	defer cleanup()

	s := NewServer(cfg, nil)
	if err := s.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer s.Stop()
	time.Sleep(100 * time.Millisecond)

	resp := queryLocal(t, cfg.ListenAddr, "foo.example.com", dns.TypeA)
	if resp.Rcode != dns.RcodeRefused {
		t.Errorf("Rcode = %d, want Refused (invalid AnswerIP)", resp.Rcode)
	}
	if s.Stats().BlockedQueries != 1 {
		t.Errorf("BlockedQueries = %d, want 1", s.Stats().BlockedQueries)
	}
	if !strings.Contains(s.Stats().LastError, "invalid answerIp") {
		t.Errorf("LastError should mention 'invalid answerIp', got: %q", s.Stats().LastError)
	}
}

func TestHandleQuery_UpstreamFails_ServFail(t *testing.T) {
	listen, _ := allocTestPorts()
	cfg := newTestConfig(listen, 1) // 故意指到不可能的端口
	cfg.DomainRules = []string{"*.example.com"}

	s := NewServer(cfg, nil)
	if err := s.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer s.Stop()
	time.Sleep(100 * time.Millisecond)

	resp := queryLocal(t, cfg.ListenAddr, "github.com", dns.TypeA)
	if resp.Rcode != dns.RcodeServerFailure {
		t.Errorf("Rcode = %d, want ServerFailure", resp.Rcode)
	}
	if s.Stats().BlockedQueries != 1 {
		t.Errorf("BlockedQueries = %d, want 1", s.Stats().BlockedQueries)
	}
}

// ---------------------------------------------------------------------------
// handleQuery 直接调 — 测 stats 累加
// ---------------------------------------------------------------------------

func TestHandleQuery_StatsAccumulation(t *testing.T) {
	s := NewServer(Config{
		AnswerIP:    "127.0.0.1",
		Upstream:    "127.0.0.1:1", // 故意不通
		DomainRules: []string{"*.hit.com"},
	}, nil)

	// 3 命中 + 2 miss
	for i := 0; i < 3; i++ {
		w := &mockResponseWriter{}
		req := new(dns.Msg)
		req.SetQuestion("a.hit.com.", dns.TypeA)
		s.handleQuery(w, req)
	}
	for i := 0; i < 2; i++ {
		w := &mockResponseWriter{}
		req := new(dns.Msg)
		req.SetQuestion("miss.com.", dns.TypeA)
		s.handleQuery(w, req)
	}

	stats := s.Stats()
	if stats.TotalQueries != 5 {
		t.Errorf("TotalQueries = %d, want 5", stats.TotalQueries)
	}
	if stats.HitQueries != 3 {
		t.Errorf("HitQueries = %d, want 3", stats.HitQueries)
	}
	if stats.BlockedQueries != 2 {
		// 2 miss → upstream 不通 → 走 replyServFail + blockedQ
		t.Errorf("BlockedQueries = %d, want 2", stats.BlockedQueries)
	}
	if stats.LastQueryAt == 0 {
		t.Error("LastQueryAt should be non-zero after queries")
	}
}

// ---------------------------------------------------------------------------
// 并发 query 后 stats
// ---------------------------------------------------------------------------

func TestStats_ConcurrentQueries(t *testing.T) {
	listen, upstream := allocTestPorts()
	cfg := newTestConfig(listen, upstream)
	cfg.DomainRules = []string{"*.example.com"}
	cleanup := startTestUpstream(t, upstream, func(w dns.ResponseWriter, r *dns.Msg) {
		w.WriteMsg(&dns.Msg{})
	})
	defer cleanup()

	s := NewServer(cfg, nil)
	if err := s.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer s.Stop()
	time.Sleep(100 * time.Millisecond)

	const N = 30
	var wg sync.WaitGroup
	for i := 0; i < N; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			queryLocal(t, cfg.ListenAddr, "foo.example.com", dns.TypeA)
		}()
	}
	wg.Wait()
	stats := s.Stats()
	if stats.TotalQueries != N {
		t.Errorf("TotalQueries = %d, want %d", stats.TotalQueries, N)
	}
	if stats.HitQueries != N {
		t.Errorf("HitQueries = %d, want %d", stats.HitQueries, N)
	}
}
