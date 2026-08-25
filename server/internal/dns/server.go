// Package dnsserver — see config.go for overview.
package dnsserver

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/miekg/dns"
)

// Server 是 mini DNS server 实例。
type Server struct {
	mu       sync.RWMutex
	cfg      Config
	udpSrv   *dns.Server
	tcpSrv   *dns.Server
	client   *dns.Client // 上游转发 client
	log      *slog.Logger

	// 统计（用 atomic 避免锁竞争）
	totalQ    atomic.Int64
	hitQ      atomic.Int64
	missQ     atomic.Int64
	blockedQ  atomic.Int64
	lastQuery atomic.Int64 // unix nano
	lastError atomic.Value // string
}

// NewServer 构造 server 实例（不启动）。
func NewServer(cfg Config, log *slog.Logger) *Server {
	if log == nil {
		log = slog.Default()
	}
	return &Server{
		cfg:    cfg,
		client: &dns.Client{Net: "udp", Timeout: 3 * time.Second},
		log:    log,
	}
}

// Start 启动 UDP + TCP 监听。
// cfg.Enabled = false 时 Start 是 no-op（不报错）。
func (s *Server) Start(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.cfg.Enabled {
		s.log.Info("dns server disabled (cfg.enabled=false)")
		return nil
	}
	if s.cfg.ListenAddr == "" {
		return errors.New("dns: listenAddr empty")
	}
	if s.cfg.AnswerIP == "" {
		return errors.New("dns: answerIp empty")
	}
	if s.cfg.Upstream == "" {
		return errors.New("dns: upstream empty")
	}
	if s.udpSrv != nil {
		return nil // 已经在跑
	}

	mux := dns.NewServeMux()
	mux.HandleFunc(".", s.handleQuery)

	s.udpSrv = &dns.Server{Addr: s.cfg.ListenAddr, Net: "udp", Handler: mux}
	s.tcpSrv = &dns.Server{Addr: s.cfg.ListenAddr, Net: "tcp", Handler: mux}

	// udp listen — 失败要清理
	udpErrCh := make(chan error, 1)
	// 同上：local 变量，避 closure nil deref
	udpSrv := s.udpSrv
	go func() {
		if err := udpSrv.ListenAndServe(); err != nil {
			udpErrCh <- err
		}
	}()
	// 等一帧确认 udp 没立刻挂
	select {
	case err := <-udpErrCh:
		s.udpSrv = nil
		return fmt.Errorf("dns udp listen %s: %w", s.cfg.ListenAddr, err)
	case <-time.After(100 * time.Millisecond):
		// ok
	}
	// tcp listen
	//
	// 注意：把 s.tcpSrv 复制到 local 变量！closure 是延迟求值 — 如果 goroutine
	// 真跑时 s.tcpSrv 已被 Stop() 设为 nil，这里会 SEGV。
	tcpSrv := s.tcpSrv
	go func() {
		if err := tcpSrv.ListenAndServe(); err != nil {
			s.log.Warn("dns tcp serve ended", "err", err)
		}
	}()

	s.log.Info("dns server started", "addr", s.cfg.ListenAddr, "upstream", s.cfg.Upstream, "answer_ip", s.cfg.AnswerIP, "rules", len(s.cfg.DomainRules))
	return nil
}

// Stop 停止监听。多次调用安全。
func (s *Server) Stop() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.udpSrv == nil {
		return nil
	}
	err1 := s.udpSrv.Shutdown()
	err2 := s.tcpSrv.Shutdown()
	s.udpSrv = nil
	s.tcpSrv = nil
	s.log.Info("dns server stopped")
	if err1 != nil {
		return err1
	}
	return err2
}

// Reload 替换配置并重启（cfg 改变时调用）。
func (s *Server) Reload(ctx context.Context, cfg Config) error {
	if err := s.Stop(); err != nil {
		s.log.Warn("dns stop during reload", "err", err)
	}
	s.mu.Lock()
	s.cfg = cfg
	s.mu.Unlock()
	return s.Start(ctx)
}

// Stats 返回当前统计快照。
func (s *Server) Stats() Stats {
	lastNano := s.lastQuery.Load()
	var lastQ int64
	if lastNano > 0 {
		lastQ = time.Unix(0, lastNano).Unix()
	}
	var lastErr string
	if v := s.lastError.Load(); v != nil {
		lastErr, _ = v.(string)
	}
	return Stats{
		TotalQueries:   s.totalQ.Load(),
		HitQueries:     s.hitQ.Load(),
		MissQueries:    s.missQ.Load(),
		BlockedQueries: s.blockedQ.Load(),
		LastQueryAt:    lastQ,
		LastError:      lastErr,
	}
}

// Config 返回当前生效配置（copy）。
func (s *Server) Config() Config {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.cfg
}

// MatchDomainForTest 暴露 matchDomain 供 API /test 端点使用。
func (s *Server) MatchDomainForTest(name string) bool {
	return s.matchDomain(name)
}

// handleQuery 是 dns.HandlerFunc 入口。
func (s *Server) handleQuery(w dns.ResponseWriter, req *dns.Msg) {
	s.totalQ.Add(1)
	s.lastQuery.Store(time.Now().UnixNano())

	if len(req.Question) == 0 {
		s.replyRefused(w, req)
		return
	}
	q := req.Question[0]
	// 只处理 A 记录（IPv4）。AAAA 转发上游。
	if q.Qtype == dns.TypeAAAA {
		s.forwardToUpstream(w, req)
		return
	}

	name := strings.ToLower(strings.TrimSuffix(q.Name, "."))

	if s.matchDomain(name) {
		// 命中白名单 → 返回 A 记录（指向本机 IP）
		s.replyLocalA(w, req)
		return
	}
	// 未命中 → 转发上游
	s.forwardToUpstream(w, req)
}

// matchDomain 检查 name 是否匹配任一白名单规则。
// 规则支持 *.example.com（通配符）和精确域名。
// name 和 rule 两边都先 TrimSuffix(".") + ToLower，方便 caller 不必预处理。
func (s *Server) matchDomain(name string) bool {
	s.mu.RLock()
	rules := s.cfg.DomainRules
	s.mu.RUnlock()
	name = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(name), "."))
	if name == "" {
		return false
	}
	for _, rule := range rules {
		rule = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(rule), "."))
		if rule == "" {
			continue
		}
		if strings.HasPrefix(rule, "*.") {
			suffix := rule[1:] // ".example.com"
			if strings.HasSuffix(name, suffix) {
				return true
			}
		} else {
			if name == rule {
				return true
			}
		}
	}
	return false
}

// replyLocalA 命中白名单 → 返回 A 记录。
func (s *Server) replyLocalA(w dns.ResponseWriter, req *dns.Msg) {
	s.mu.RLock()
	answerIP := s.cfg.AnswerIP
	s.mu.RUnlock()

	ip := net.ParseIP(answerIP)
	if ip == nil {
		s.lastError.Store("invalid answerIp: " + answerIP)
		s.blockedQ.Add(1)
		s.replyRefused(w, req)
		return
	}
	resp := new(dns.Msg)
	resp.SetReply(req)
	rr := &dns.A{
		Hdr: dns.RR_Header{Name: req.Question[0].Name, Rrtype: dns.TypeA, Class: dns.ClassINET, Ttl: 60},
		A:   ip,
	}
	resp.Answer = []dns.RR{rr}
	// 先累加 hit 计数再 WriteMsg — 否则 client 收到响应后立即查 stats 会读 0
	s.hitQ.Add(1)
	_ = w.WriteMsg(resp)
}

// forwardToUpstream 转发到上游 DNS。
func (s *Server) forwardToUpstream(w dns.ResponseWriter, req *dns.Msg) {
	s.mu.RLock()
	upstream := s.cfg.Upstream
	s.mu.RUnlock()
	if upstream == "" {
		s.blockedQ.Add(1)
		s.replyRefused(w, req)
		return
	}
	resp, _, err := s.client.Exchange(req, upstream)
	if err != nil {
		s.lastError.Store("upstream exchange: " + err.Error())
		s.blockedQ.Add(1)
		s.replyServFail(w, req)
		return
	}
	if resp == nil {
		s.blockedQ.Add(1)
		s.replyServFail(w, req)
		return
	}
	// 先累加 miss 计数再 WriteMsg — 同 replyLocalA，避免 client 读 stats 的 race
	s.missQ.Add(1)
	_ = w.WriteMsg(resp)
}

func (s *Server) replyRefused(w dns.ResponseWriter, req *dns.Msg) {
	resp := new(dns.Msg)
	resp.SetReply(req)
	resp.Rcode = dns.RcodeRefused
	_ = w.WriteMsg(resp)
}

func (s *Server) replyServFail(w dns.ResponseWriter, req *dns.Msg) {
	resp := new(dns.Msg)
	resp.SetReply(req)
	resp.Rcode = dns.RcodeServerFailure
	_ = w.WriteMsg(resp)
}
