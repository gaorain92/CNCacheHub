package api

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"strings"
	"time"

	dnsserver "github.com/cncachehub/server/internal/dns"
	"github.com/cncachehub/server/internal/storage"
	"github.com/miekg/dns"
)

// DNSConfigResponse 是 GET /api/dns/config 返回结构。
type DNSConfigResponse struct {
	Config    storage.DNSConfig    `json:"config"`
	Stats     dnsserver.Stats      `json:"stats"`
	Listening bool                 `json:"listening"` // server 是否在监听（cfg.enabled && server 启动成功）
}

// DNSConfigPatchRequest 是 PATCH /api/dns/config 请求体（部分更新）。
type DNSConfigPatchRequest struct {
	Enabled     *bool     `json:"enabled,omitempty"`
	ListenAddr  *string   `json:"listenAddr,omitempty"`
	Upstream    *string   `json:"upstream,omitempty"`
	AnswerIP    *string   `json:"answerIp,omitempty"`
	DomainRules *[]string `json:"domainRules,omitempty"`
}

// dnsConfigGetHandler GET /api/dns/config（admin）
func dnsConfigGetHandler(opts Options) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !requireAdmin(opts, w, r) {
			return
		}
		cfg, err := opts.GetDNSConfig(r.Context())
		if err != nil {
			writeInternalErr(w, r, "dns_config_get_failed", err)
			return
		}
		writeJSON(w, http.StatusOK, DNSConfigResponse{
			Config:    cfg,
			Stats:     opts.DNSServer.Stats(),
			Listening: opts.DNSServer.Config().Enabled,
		})
	}
}

// dnsConfigPatchHandler PATCH /api/dns/config（admin）
// 改完自动触发 DNSServer.Reload。
func dnsConfigPatchHandler(opts Options) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !requireAdmin(opts, w, r) {
			return
		}
		var req DNSConfigPatchRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid_json", err.Error())
			return
		}
		// 校验
		if req.ListenAddr != nil {
			if _, _, err := net.SplitHostPort(*req.ListenAddr); err != nil {
				writeError(w, http.StatusBadRequest, "invalid_listen_addr", "listenAddr must be host:port")
				return
			}
		}
		if req.Upstream != nil {
			if _, _, err := net.SplitHostPort(*req.Upstream); err != nil {
				writeError(w, http.StatusBadRequest, "invalid_upstream", "upstream must be host:port")
				return
			}
		}
		if req.AnswerIP != nil && net.ParseIP(*req.AnswerIP) == nil {
			writeError(w, http.StatusBadRequest, "invalid_answer_ip", "answerIp must be valid IPv4/IPv6")
			return
		}
		if req.DomainRules != nil {
			for _, rule := range *req.DomainRules {
				rule = strings.TrimSpace(rule)
				if rule == "" {
					continue
				}
				if strings.Contains(rule, " ") {
					writeError(w, http.StatusBadRequest, "invalid_domain_rule", "rule cannot contain spaces: "+rule)
					return
				}
			}
		}

		patch := storage.DNSConfigPatch{
			Enabled:     req.Enabled,
			ListenAddr:  req.ListenAddr,
			Upstream:    req.Upstream,
			AnswerIP:    req.AnswerIP,
			DomainRules: req.DomainRules,
		}
		cfg, err := opts.UpdateDNSConfig(r.Context(), patch)
		if err != nil {
			writeInternalErr(w, r, "dns_config_update_failed", err)
			return
		}
		// Reload DNS server
		if err := opts.DNSServer.Reload(r.Context(), dnsserver.Config{
			Enabled:     cfg.Enabled,
			ListenAddr:  cfg.ListenAddr,
			Upstream:    cfg.Upstream,
			AnswerIP:    cfg.AnswerIP,
			DomainRules: cfg.DomainRules,
			UpdatedAt:   time.Unix(cfg.UpdatedAt, 0),
		}); err != nil {
			writeInternalErr(w, r, "dns_reload_failed", err)
			return
		}
		writeJSON(w, http.StatusOK, DNSConfigResponse{
			Config:    cfg,
			Stats:     opts.DNSServer.Stats(),
			Listening: opts.DNSServer.Config().Enabled,
		})
	}
}

// dnsTestRequest POST /api/dns/test
type dnsTestRequest struct {
	Domain string `json:"domain"`
}
type dnsTestAnswer struct {
	Name string `json:"name"`
	Type uint16 `json:"type"`
	TTL  uint32 `json:"ttl"`
	Data string `json:"data"`
}
type dnsTestResponse struct {
	Domain    string          `json:"domain"`
	Matched   bool            `json:"matched"` // 命中白名单
	Server    string          `json:"server"`  // 实际查询的 DNS
	Rcode     string          `json:"rcode"`
	Answers   []dnsTestAnswer `json:"answers"`
	LatencyMs int64           `json:"latencyMs"`
	Error     string          `json:"error,omitempty"`
}

func dnsTestHandler(opts Options) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !requireAdmin(opts, w, r) {
			return
		}
		// POST 拿 body；GET 拿 query
		var domain string
		if r.Method == http.MethodGet {
			domain = r.URL.Query().Get("domain")
		} else {
			var req dnsTestRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				writeError(w, http.StatusBadRequest, "invalid_json", err.Error())
				return
			}
			domain = req.Domain
		}
		domain = strings.ToLower(strings.TrimSpace(strings.TrimSuffix(domain, ".")))
		if domain == "" {
			writeError(w, http.StatusBadRequest, "empty_domain", "domain required")
			return
		}

		cfg := opts.DNSServer.Config()
		target := cfg.Upstream
		if cfg.Enabled {
			target = cfg.ListenAddr
		}
		if strings.HasPrefix(target, "0.0.0.0:") {
			target = "127.0.0.1" + target[len("0.0.0.0"):]
		}

		resp := &dnsTestResponse{Domain: domain, Server: target}
		resp.Matched = cfg.Enabled && opts.DNSServer.MatchDomainForTest(domain)

		m := new(dns.Msg)
		m.SetQuestion(dns.Fqdn(domain), dns.TypeA)
		c := &dns.Client{Net: "udp", Timeout: 3 * time.Second}
		start := time.Now()
		reply, _, err := c.Exchange(m, target)
		elapsed := time.Since(start)
		resp.LatencyMs = elapsed.Milliseconds()
		if err != nil {
			resp.Rcode = "SERVFAIL"
			resp.Error = err.Error()
			writeJSON(w, http.StatusOK, resp)
			return
		}
		if reply == nil {
			resp.Rcode = "SERVFAIL"
			resp.Error = "empty reply"
			writeJSON(w, http.StatusOK, resp)
			return
		}
		resp.Rcode = dns.RcodeToString[reply.Rcode]
		for _, rr := range reply.Answer {
			switch a := rr.(type) {
			case *dns.A:
				resp.Answers = append(resp.Answers, dnsTestAnswer{
					Name: a.Hdr.Name, Type: a.Hdr.Rrtype, TTL: a.Hdr.Ttl, Data: a.A.String(),
				})
			case *dns.CNAME:
				resp.Answers = append(resp.Answers, dnsTestAnswer{
					Name: a.Hdr.Name, Type: a.Hdr.Rrtype, TTL: a.Hdr.Ttl, Data: a.Target,
				})
			case *dns.AAAA:
				resp.Answers = append(resp.Answers, dnsTestAnswer{
					Name: a.Hdr.Name, Type: a.Hdr.Rrtype, TTL: a.Hdr.Ttl, Data: a.AAAA.String(),
				})
			}
		}
		writeJSON(w, http.StatusOK, resp)
	}
}

// dnsStatsHandler GET /api/dns/stats
func dnsStatsHandler(opts Options) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !requireAdmin(opts, w, r) {
			return
		}
		writeJSON(w, http.StatusOK, opts.DNSServer.Stats())
	}
}

// requireAdmin 简易 RBAC 校验。
//
// nil-safe：opts.SessionUserRole == nil 时返 503（不是 panic）。
func requireAdmin(opts Options, w http.ResponseWriter, r *http.Request) bool {
	if opts.SessionUserRole == nil {
		writeError(w, http.StatusServiceUnavailable, "auth_unavailable", "session user role not configured")
		return false
	}
	role, _, _ := opts.SessionUserRole(r.Context(), r)
	if role != "admin" {
		writeError(w, http.StatusForbidden, "admin_required", "this endpoint requires admin")
		return false
	}
	return true
}

// 防止 context 包 import 警告（保留）
var _ = context.Background
