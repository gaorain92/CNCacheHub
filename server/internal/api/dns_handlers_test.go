package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"

	dnsserver "github.com/cncachehub/server/internal/dns"
	"github.com/cncachehub/server/internal/storage"
	"github.com/go-chi/chi/v5"
)

// fakeDNSConfigDB 满足 dns handler 需要的最小 DB 接口（GetDNSConfig / UpdateDNSConfig）。
type fakeDNSConfigDB struct {
	mu      sync.Mutex
	cfg     storage.DNSConfig
	getErr  error
	setErr  error
	getHits int
	setHits int
}

func (f *fakeDNSConfigDB) GetDNSConfig(ctx context.Context) (storage.DNSConfig, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.getHits++
	if f.getErr != nil {
		return storage.DNSConfig{}, f.getErr
	}
	return f.cfg, nil
}

func (f *fakeDNSConfigDB) UpdateDNSConfig(ctx context.Context, patch storage.DNSConfigPatch) (storage.DNSConfig, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.setHits++
	if f.setErr != nil {
		return storage.DNSConfig{}, f.setErr
	}
	if patch.Enabled != nil {
		f.cfg.Enabled = *patch.Enabled
	}
	if patch.AnswerIP != nil {
		f.cfg.AnswerIP = *patch.AnswerIP
	}
	if patch.DomainRules != nil {
		f.cfg.DomainRules = *patch.DomainRules
	}
	return f.cfg, nil
}

// fakeRoleChecker 返回固定角色 + userID。
type fakeRoleChecker struct {
	role  string
	uid   int64
	calls int
}

func (f *fakeRoleChecker) SessionUserRole(ctx context.Context, r *http.Request) (string, int64, error) {
	f.calls++
	return f.role, f.uid, nil
}

// buildDNSRouter 构造只含 dns 路由的测试 chi 实例。
func buildDNSRouter(t *testing.T, db *fakeDNSConfigDB, rc *fakeRoleChecker, dnsSrv *dnsserver.Server) http.Handler {
	t.Helper()
	r := chi.NewRouter()
	opts := Options{
		GetDNSConfig:     db.GetDNSConfig,
		UpdateDNSConfig:  db.UpdateDNSConfig,
		DNSServer:        dnsSrv,
		SessionUserRole:  rc.SessionUserRole,
	}
	r.Get("/api/dns/config", dnsConfigGetHandler(opts))
	r.Patch("/api/dns/config", dnsConfigPatchHandler(opts))
	r.Get("/api/dns/stats", dnsStatsHandler(opts))
	r.Get("/api/dns/test", dnsTestHandler(opts))
	r.Post("/api/dns/test", dnsTestHandler(opts))
	return r
}

func TestDNSConfig_GetRequiresAdmin(t *testing.T) {
	db := &fakeDNSConfigDB{cfg: storage.DNSConfig{ID: 1, ListenAddr: "0.0.0.0:5353", Upstream: "1.1.1.1:53", AnswerIP: "127.0.0.1", DomainRules: []string{"*.steamcontent.com"}}}
	rc := &fakeRoleChecker{role: "user"} // 非 admin
	srv := dnsserver.NewServer(dnsserver.DefaultConfig(), slog.Default())
	r := buildDNSRouter(t, db, rc, srv)
	req := httptest.NewRequest("GET", "/api/dns/config", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Errorf("code = %d, want 403", rr.Code)
	}
}

func TestDNSConfig_GetAsAdmin(t *testing.T) {
	db := &fakeDNSConfigDB{cfg: storage.DNSConfig{ID: 1, Enabled: true, ListenAddr: "0.0.0.0:5353", Upstream: "1.1.1.1:53", AnswerIP: "127.0.0.1", DomainRules: []string{"*.steamcontent.com"}}}
	rc := &fakeRoleChecker{role: "admin", uid: 1}
	srv := dnsserver.NewServer(dnsserver.DefaultConfig(), slog.Default())
	r := buildDNSRouter(t, db, rc, srv)
	req := httptest.NewRequest("GET", "/api/dns/config", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("code = %d, body=%s", rr.Code, rr.Body.String())
	}
	var resp DNSConfigResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("json: %v", err)
	}
	if !resp.Config.Enabled {
		t.Error("Enabled should be true")
	}
	if len(resp.Config.DomainRules) != 1 {
		t.Errorf("DomainRules len = %d, want 1", len(resp.Config.DomainRules))
	}
}

func TestDNSConfig_PatchValidation(t *testing.T) {
	cases := []struct {
		name string
		body string
		code int
	}{
		{"invalid_listen_addr", `{"listenAddr":"no-port"}`, http.StatusBadRequest},
		{"invalid_upstream", `{"upstream":"no-port"}`, http.StatusBadRequest},
		{"invalid_answer_ip", `{"answerIp":"999.999.999.999"}`, http.StatusBadRequest},
		{"rule_with_space", `{"domainRules":["bad rule.com"]}`, http.StatusBadRequest},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			db := &fakeDNSConfigDB{cfg: storage.DNSConfig{ID: 1, ListenAddr: "0.0.0.0:5353", Upstream: "1.1.1.1:53", AnswerIP: "127.0.0.1"}}
			rc := &fakeRoleChecker{role: "admin"}
			srv := dnsserver.NewServer(dnsserver.DefaultConfig(), slog.Default())
			r := buildDNSRouter(t, db, rc, srv)
			req := httptest.NewRequest("PATCH", "/api/dns/config", bytes.NewBufferString(c.body))
			rr := httptest.NewRecorder()
			r.ServeHTTP(rr, req)
			if rr.Code != c.code {
				t.Errorf("code = %d, want %d, body=%s", rr.Code, c.code, rr.Body.String())
			}
		})
	}
}

func TestDNSConfig_PatchSuccess(t *testing.T) {
	// 用独立端口避免与 IntegrationWithRealDB 抢 5353
	db := &fakeDNSConfigDB{cfg: storage.DNSConfig{ID: 1, ListenAddr: "127.0.0.1:15353", Upstream: "1.1.1.1:53", AnswerIP: "127.0.0.1"}}
	rc := &fakeRoleChecker{role: "admin"}
	srv := dnsserver.NewServer(dnsserver.DefaultConfig(), slog.Default())
	r := buildDNSRouter(t, db, rc, srv)
	body := `{"enabled":true,"answerIp":"192.168.1.10","domainRules":["*.steamcontent.com"]}`
	req := httptest.NewRequest("PATCH", "/api/dns/config", bytes.NewBufferString(body))
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("code = %d, body=%s", rr.Code, rr.Body.String())
	}
	// 清理
	_ = srv.Stop()
	if db.setHits != 1 {
		t.Errorf("setHits = %d, want 1", db.setHits)
	}
	if !db.cfg.Enabled {
		t.Error("Enabled not persisted")
	}
	if db.cfg.AnswerIP != "192.168.1.10" {
		t.Errorf("AnswerIP = %q, want 192.168.1.10", db.cfg.AnswerIP)
	}
}

func TestDNSConfig_GetDBError(t *testing.T) {
	db := &fakeDNSConfigDB{getErr: errors.New("db down")}
	rc := &fakeRoleChecker{role: "admin"}
	srv := dnsserver.NewServer(dnsserver.DefaultConfig(), slog.Default())
	r := buildDNSRouter(t, db, rc, srv)
	req := httptest.NewRequest("GET", "/api/dns/config", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusInternalServerError {
		t.Errorf("code = %d, want 500", rr.Code)
	}
}

// 集成测试用真 DB（验证 storage 层到 handler 整条链）
func TestDNSConfig_IntegrationWithRealDB(t *testing.T) {
	dir := t.TempDir()
	db, err := storage.Open(context.Background(), filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("storage.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	opts := Options{
		GetDNSConfig:     db.GetDNSConfig,
		UpdateDNSConfig:  db.UpdateDNSConfig,
		DNSServer:        dnsserver.NewServer(dnsserver.DefaultConfig(), slog.Default()),
		SessionUserRole:  func(ctx context.Context, r *http.Request) (string, int64, error) { return "admin", 1, nil },
	}
	// integration test 用独立端口避免冲突
	// 写一个独立 dns server，用 15354
	dnsSrv := dnsserver.NewServer(dnsserver.Config{
		Enabled: false, ListenAddr: "127.0.0.1:15354", Upstream: "1.1.1.1:53", AnswerIP: "127.0.0.1", DomainRules: []string{},
	}, slog.Default())
	opts.DNSServer = dnsSrv
	t.Cleanup(func() { _ = dnsSrv.Stop() })
	r := chi.NewRouter()
	r.Get("/api/dns/config", dnsConfigGetHandler(opts))
	r.Patch("/api/dns/config", dnsConfigPatchHandler(opts))

	// 1. 默认值
	req := httptest.NewRequest("GET", "/api/dns/config", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("GET default: %d", rr.Code)
	}
	var resp DNSConfigResponse
	_ = json.Unmarshal(rr.Body.Bytes(), &resp)
	if resp.Config.Upstream != "1.1.1.1:53" {
		t.Errorf("default Upstream = %q", resp.Config.Upstream)
	}
	// 2. PATCH — 注意 enabled=true 会真的尝试 listen，要确保端口没占用
	patchBody := `{"enabled":true,"answerIp":"10.0.0.1"}`
	// 先改 ListenAddr 到独立端口（避免冲突）
	pre := `{"listenAddr":"127.0.0.1:15355"}`
	req = httptest.NewRequest("PATCH", "/api/dns/config", bytes.NewBufferString(pre))
	rr = httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("PATCH pre: %d body=%s", rr.Code, rr.Body.String())
	}
	_ = rr

	req = httptest.NewRequest("PATCH", "/api/dns/config", bytes.NewBufferString(patchBody))
	rr = httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("PATCH: %d body=%s", rr.Code, rr.Body.String())
	}
	// 3. 再 GET 验证
	req = httptest.NewRequest("GET", "/api/dns/config", nil)
	rr = httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	_ = json.Unmarshal(rr.Body.Bytes(), &resp)
	if !resp.Config.Enabled {
		t.Error("Enabled not persisted after PATCH")
	}
	if resp.Config.AnswerIP != "10.0.0.1" {
		t.Errorf("AnswerIP = %q, want 10.0.0.1", resp.Config.AnswerIP)
	}
}
