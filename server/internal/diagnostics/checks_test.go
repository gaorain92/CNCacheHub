package diagnostics

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestCheckUpstreamReachable_Empty(t *testing.T) {
	r := checkUpstreamReachable("")
	if r.Status != StatusWarning {
		t.Errorf("status = %q, want warning (empty upstream)", r.Status)
	}
}

func TestCheckUpstreamReachable_BadURL(t *testing.T) {
	r := checkUpstreamReachable("://not a url")
	if r.Status == StatusOK {
		t.Error("bad url should not be ok")
	}
}

func TestCheckUpstreamReachable_Refused(t *testing.T) {
	// 127.0.0.1:1 一定 refused
	r := checkUpstreamReachable("http://127.0.0.1:1")
	if r.Status != StatusError {
		t.Errorf("status = %q, want error (refused)", r.Status)
	}
}

func TestRollupSummary(t *testing.T) {
	cases := []struct {
		name string
		in   []CheckResult
		want string
	}{
		{"empty", nil, StatusOK},
		{"all_ok", []CheckResult{{Status: StatusOK}, {Status: StatusOK}}, StatusOK},
		{"one_warning", []CheckResult{{Status: StatusOK}, {Status: StatusWarning}}, StatusWarning},
		{"one_error", []CheckResult{{Status: StatusWarning}, {Status: StatusError}}, StatusError},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := rollupSummary(c.in); got != c.want {
				t.Errorf("got %q, want %q", got, c.want)
			}
		})
	}
}

func TestContains(t *testing.T) {
	if !contains([]string{"a", "b", "c"}, "b") {
		t.Error("contains b should be true")
	}
	if contains([]string{"a", "b"}, "z") {
		t.Error("contains z should be false")
	}
}

func TestCheckDaemonConfig_Empty(t *testing.T) {
	r := checkDaemonConfig(RunnerOptions{
		PublicBaseURL: "http://117.55.237.250",
		DaemonConfig:  func() (mirrors []string, insecure bool) { return nil, false },
	})
	if r.Status != StatusWarning {
		t.Errorf("status = %q, want warning", r.Status)
	}
}

func TestCheckDaemonConfig_OK(t *testing.T) {
	r := checkDaemonConfig(RunnerOptions{
		PublicBaseURL: "http://117.55.237.250",
		DaemonConfig: func() (mirrors []string, insecure bool) {
			return []string{"http://117.55.237.250"}, false
		},
	})
	if r.Status != StatusOK {
		t.Errorf("status = %q, want ok; msg=%s", r.Status, r.Message)
	}
}

func TestCheckDockerPull_EmptyOpts(t *testing.T) {
	// 空 opts — 不应 panic，至少跑完不 crash
	r := CheckDockerPull(context.Background(), RunnerOptions{})
	if len(r.Checks) == 0 {
		t.Error("expected some checks even with empty opts")
	}
	// Upstream 应该是 warning（未配置）
	found := false
	for _, c := range r.Checks {
		if c.Name == "上游 registry 可达" && c.Status == StatusWarning {
			found = true
		}
	}
	if !found {
		t.Error("upstream check should be warning when URL empty")
	}
}

func TestCheckSteamDNS_NoCallbacks(t *testing.T) {
	r := CheckSteamDNS(context.Background(), RunnerOptions{})
	if r.Summary != StatusWarning {
		t.Errorf("summary = %q, want warning", r.Summary)
	}
}

func TestCheckPublicV2_SPA(t *testing.T) {
	// 模拟 nginx SPA fallback（返回 text/html）— 应当 error
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(200)
		_, _ = w.Write([]byte("<html>index</html>"))
	}))
	defer srv.Close()
	r := checkPublicV2(srv.URL)
	if r.Status != StatusError {
		t.Errorf("status = %q, want error (SPA fallback eats /v2/)", r.Status)
	}
}

func TestCheckPublicV2_OK(t *testing.T) {
	// 模拟正常 /v2/ 401
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(401)
		_, _ = w.Write([]byte(`{"error":"unauthorized"}`))
	}))
	defer srv.Close()
	r := checkPublicV2(srv.URL)
	if r.Status != StatusOK {
		t.Errorf("status = %q, want ok; msg=%s", r.Status, r.Message)
	}
}

func TestRunAll_NoCrash(t *testing.T) {
	// RunAll 串行跑 3 剧本，全部不 panic 就行（不管结果）
	fr := RunAll(context.Background(), RunnerOptions{
		PublicBaseURL: "http://127.0.0.1:1", // 故意失败
		CNCHBaseURL:   "http://127.0.0.1:1",
		UpstreamURL:   "http://127.0.0.1:1",
		DNSServerStats: func() DNSStats {
			return DNSStats{Enabled: true, ListenAddr: "127.0.0.1:15353", DomainRules: []string{"*.steamcontent.com"}}
		},
		AccessLogCount: func() (int, int, int, error) { return 0, 0, 0, nil },
		DaemonConfig:   func() ([]string, bool) { return nil, false },
	})
	if len(fr.Playbooks) != 3 {
		t.Errorf("playbooks = %d, want 3", len(fr.Playbooks))
	}
}

// 防止 unused import warning
var _ = filepath.Join
var _ = os.Getenv
