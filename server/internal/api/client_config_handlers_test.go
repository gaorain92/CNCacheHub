package api

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/cncachehub/server/internal/storage"
)

func clientConfigTestHandler(t *testing.T) (http.Handler, *fakeAuthDB) {
	t.Helper()
	fdb := newFakeAuthDB(t)
	_, _ = fdb.CreateUser(context.Background(), "admin", "admin1234", true)
	return NewRouter(Options{
		AuthDB: fdb.DB,
		ListRegistries: func(ctx context.Context) ([]storage.Registry, error) {
			return []storage.Registry{
				{ID: 1, Name: "dockerhub", UpstreamURL: "https://registry-1.docker.io", MirrorPath: "", Enabled: true},
				{ID: 2, Name: "ghcr", UpstreamURL: "https://ghcr.io", MirrorPath: "/v2/ghcr", Enabled: true},
				{ID: 3, Name: "quay", UpstreamURL: "https://quay.io", MirrorPath: "/v2/quay", Enabled: true},
				{ID: 4, Name: "k8s", UpstreamURL: "https://registry.k8s.io", MirrorPath: "/v2/k8s", Enabled: true},
				{ID: 5, Name: "disabled-test", UpstreamURL: "https://example.com", MirrorPath: "/v2/dt", Enabled: false},
			}, nil
		},
		GetSettings: func(ctx context.Context) (SystemSettings, error) {
			return SystemSettings{}, nil
		},
	}), fdb
}

func TestClientConfig_ContainerdHosts_GHCR(t *testing.T) {
	h, _ := clientConfigTestHandler(t)
	req := httptest.NewRequest(http.MethodPost, "/api/client-config?format=containerd-hosts&registry=ghcr", nil)
	req.Host = "117.55.237.250:8082"
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	var resp clientConfigResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Registry != "ghcr" {
		t.Errorf("Registry = %q, want ghcr", resp.Registry)
	}
	if resp.Hostname != "ghcr.io" {
		t.Errorf("Hostname = %q, want ghcr.io", resp.Hostname)
	}
	if !strings.Contains(resp.Content, `server = "https://ghcr.io"`) {
		t.Errorf("content missing server = https://ghcr.io; got: %s", resp.Content)
	}
	if !strings.Contains(resp.Content, `[host."http://117.55.237.250:8082"]`) {
		t.Errorf("content missing [host.]; got: %s", resp.Content)
	}
	if !strings.Contains(resp.Content, "capabilities") {
		t.Errorf("content missing capabilities")
	}
	if resp.TargetPath != "/etc/containerd/certs.d/ghcr.io/hosts.toml" {
		t.Errorf("TargetPath = %q, want /etc/containerd/certs.d/ghcr.io/hosts.toml", resp.TargetPath)
	}
	if resp.RestartCmd != "systemctl restart containerd" {
		t.Errorf("RestartCmd = %q", resp.RestartCmd)
	}
}

func TestClientConfig_K3sRegistries_GHCR(t *testing.T) {
	h, _ := clientConfigTestHandler(t)
	req := httptest.NewRequest(http.MethodPost, "/api/client-config?format=k3s-registries&registry=ghcr", nil)
	req.Host = "117.55.237.250:8082"
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	var resp clientConfigResponse
	_ = json.Unmarshal(rr.Body.Bytes(), &resp)
	if !strings.Contains(resp.Content, "mirrors:") {
		t.Errorf("content missing mirrors:")
	}
	if !strings.Contains(resp.Content, "ghcr.io:") {
		t.Errorf("content missing ghcr.io: block")
	}
	if !strings.Contains(resp.Content, "http://117.55.237.250:8082") {
		t.Errorf("content missing endpoint URL")
	}
	if resp.TargetPath != "/etc/rancher/k3s/registries.yaml" {
		t.Errorf("TargetPath = %q, want /etc/rancher/k3s/registries.yaml", resp.TargetPath)
	}
}

func TestClientConfig_DefaultHost_ForDockerhub(t *testing.T) {
	h, _ := clientConfigTestHandler(t)
	// dockerhub 用 /v2 (无 ghcr/ 前缀) — 客户端访问时镜像路径应该是根
	req := httptest.NewRequest(http.MethodPost, "/api/client-config?format=containerd-hosts&registry=dockerhub", nil)
	req.Host = "117.55.237.250:8082"
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	var resp clientConfigResponse
	_ = json.Unmarshal(rr.Body.Bytes(), &resp)
	if resp.Hostname != "docker.io" {
		t.Errorf("Hostname = %q, want docker.io", resp.Hostname)
	}
	if !strings.Contains(resp.Content, `server = "https://docker.io"`) {
		t.Errorf("content missing docker.io")
	}
	if !strings.Contains(resp.Content, "/etc/containerd/certs.d/docker.io/hosts.toml") == false {
		// 验证 target path 在注释里
		if !strings.Contains(resp.Content, "docker.io") {
			t.Errorf("content missing docker.io reference")
		}
	}
}

func TestClientConfig_Disabled_400(t *testing.T) {
	h, _ := clientConfigTestHandler(t)
	req := httptest.NewRequest(http.MethodPost, "/api/client-config?format=containerd-hosts&registry=disabled-test", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 (registry disabled)", rr.Code)
	}
}

func TestClientConfig_MissingFormat_400(t *testing.T) {
	h, _ := clientConfigTestHandler(t)
	req := httptest.NewRequest(http.MethodPost, "/api/client-config?registry=ghcr", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rr.Code)
	}
}

func TestClientConfig_UnknownFormat_400(t *testing.T) {
	h, _ := clientConfigTestHandler(t)
	req := httptest.NewRequest(http.MethodPost, "/api/client-config?format=invalid&registry=ghcr", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rr.Code)
	}
}

func TestClientConfig_UnknownRegistry_404(t *testing.T) {
	h, _ := clientConfigTestHandler(t)
	req := httptest.NewRequest(http.MethodPost, "/api/client-config?format=containerd-hosts&registry=nonexistent", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rr.Code)
	}
}

// === §9.5.4 bundle tests ===

// extractBundle 解 zip 返回 map[path]→content。
func extractBundle(t *testing.T, data []byte) map[string][]byte {
	t.Helper()
	r, err := zip.NewReader(strings.NewReader(string(data)), int64(len(data)))
	if err != nil {
		t.Fatalf("zip.NewReader: %v", err)
	}
	out := make(map[string][]byte, len(r.File))
	for _, f := range r.File {
		rc, err := f.Open()
		if err != nil {
			t.Fatalf("open %s: %v", f.Name, err)
		}
		body, _ := io.ReadAll(rc)
		_ = rc.Close()
		out[f.Name] = body
	}
	return out
}

func TestClientConfigBundle_HasAll10Files(t *testing.T) {
	h, _ := clientConfigTestHandler(t)
	req := httptest.NewRequest(http.MethodPost, "/api/client-config/bundle", nil)
	req.Host = "117.55.237.250:8082"
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	if ct := rr.Header().Get("Content-Type"); ct != "application/zip" {
		t.Errorf("Content-Type = %q, want application/zip", ct)
	}
	if cd := rr.Header().Get("Content-Disposition"); !strings.HasPrefix(cd, `attachment; filename="cncachehub-client-config-`) {
		t.Errorf("Content-Disposition = %q, want attachment with cncachehub-client-config- prefix", cd)
	}

	files := extractBundle(t, rr.Body.Bytes())
	want := []string{
		"docker/daemon.json",
		"containerd/docker.io/hosts.toml",
		"containerd/ghcr.io/hosts.toml",
		"containerd/quay.io/hosts.toml",
		"containerd/registry.k8s.io/hosts.toml",
		"k3s/registries.yaml",
		"steamcmd/docker-compose.yml",
		"resource-accelerators/playwright.env",
		"resource-accelerators/puppeteer.env",
		"resource-accelerators/terraformrc",
		"resource-accelerators/helm-repos.sh",
		"verify.sh",
		"README.md",
	}
	for _, p := range want {
		if _, ok := files[p]; !ok {
			t.Errorf("missing file in bundle: %s", p)
		}
	}
	// disabled-test 不应出现在 containerd/ 下
	if _, ok := files["containerd/example.com/hosts.toml"]; ok {
		t.Errorf("disabled registry should not appear in bundle")
	}
}

func TestClientConfigBundle_DaemonJSON_Valid(t *testing.T) {
	h, _ := clientConfigTestHandler(t)
	req := httptest.NewRequest(http.MethodPost, "/api/client-config/bundle", nil)
	req.Host = "117.55.237.250:8082"
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d", rr.Code)
	}

	files := extractBundle(t, rr.Body.Bytes())
	dj, ok := files["docker/daemon.json"]
	if !ok {
		t.Fatal("docker/daemon.json missing")
	}
	// 应该含 registry-mirrors
	if !strings.Contains(string(dj), `"registry-mirrors"`) {
		t.Errorf("daemon.json missing registry-mirrors: %s", dj)
	}
	if !strings.Contains(string(dj), `http://117.55.237.250:8082`) {
		t.Errorf("daemon.json missing base URL: %s", dj)
	}
	// 应该是合法 JSON（去掉注释前缀行）
	lines := strings.Split(string(dj), "\n")
	// 提取花括号部分
	start := -1
	for i, l := range lines {
		if strings.HasPrefix(strings.TrimSpace(l), "{") {
			start = i
			break
		}
	}
	if start < 0 {
		t.Fatal("daemon.json: no JSON start")
	}
	body := strings.Join(lines[start:], "\n")
	var parsed map[string]any
	if err := json.Unmarshal([]byte(body), &parsed); err != nil {
		t.Fatalf("daemon.json not valid JSON: %v\n%s", err, body)
	}
	mirrors, ok := parsed["registry-mirrors"].([]any)
	if !ok || len(mirrors) == 0 {
		t.Errorf("daemon.json registry-mirrors empty")
	}
}

func TestClientConfigBundle_K3sYAML_ContainsAllEnabled(t *testing.T) {
	h, _ := clientConfigTestHandler(t)
	req := httptest.NewRequest(http.MethodPost, "/api/client-config/bundle", nil)
	req.Host = "117.55.237.250:8082"
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d", rr.Code)
	}
	files := extractBundle(t, rr.Body.Bytes())
	ky := string(files["k3s/registries.yaml"])
	// 应该含 4 个 enabled registry（key 用 %q 引用）
	for _, h := range []string{`"docker.io":`, `"ghcr.io":`, `"quay.io":`, `"registry.k8s.io":`} {
		if !strings.Contains(ky, h) {
			t.Errorf("k3s yaml missing %s", h)
		}
	}
}

func TestClientConfigBundle_PlaywrightEnv_ContainsHost(t *testing.T) {
	h, _ := clientConfigTestHandler(t)
	req := httptest.NewRequest(http.MethodPost, "/api/client-config/bundle", nil)
	req.Host = "117.55.237.250:8082"
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	files := extractBundle(t, rr.Body.Bytes())
	pe := string(files["resource-accelerators/playwright.env"])
	if !strings.Contains(pe, "PLAYWRIGHT_DOWNLOAD_HOST=") {
		t.Errorf("playwright.env missing PLAYWRIGHT_DOWNLOAD_HOST")
	}
	if !strings.Contains(pe, "http://117.55.237.250:8082/r/playwright") {
		t.Errorf("playwright.env missing base URL")
	}
}

func TestClientConfigBundle_VerifySh_BashSyntax(t *testing.T) {
	// 跳过 bash 语法检查（不是所有环境都有 bash）
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not available")
	}
	h, _ := clientConfigTestHandler(t)
	req := httptest.NewRequest(http.MethodPost, "/api/client-config/bundle", nil)
	req.Host = "117.55.237.250:8082"
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	files := extractBundle(t, rr.Body.Bytes())
	vs := files["verify.sh"]
	tmp := t.TempDir() + "/verify.sh"
	if err := os.WriteFile(tmp, vs, 0644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("bash", "-n", tmp)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Errorf("bash -n failed: %v\noutput: %s\nscript:\n%s", err, out, vs)
	}
}

func TestClientConfigBundle_Readme_ContainsBaseURL(t *testing.T) {
	h, _ := clientConfigTestHandler(t)
	req := httptest.NewRequest(http.MethodPost, "/api/client-config/bundle", nil)
	req.Host = "117.55.237.250:8082"
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	files := extractBundle(t, rr.Body.Bytes())
	rm := string(files["README.md"])
	if !strings.Contains(rm, "http://117.55.237.250:8082") {
		t.Errorf("README missing base URL")
	}
	if !strings.Contains(rm, "## 快速验证") {
		t.Errorf("README missing 快速验证 section")
	}
	if !strings.Contains(rm, "verify.sh") {
		t.Errorf("README missing verify.sh reference")
	}
}

func TestClientConfigBundle_SteamCMDCompose_ContainsDNS(t *testing.T) {
	h, _ := clientConfigTestHandler(t)
	req := httptest.NewRequest(http.MethodPost, "/api/client-config/bundle", nil)
	req.Host = "117.55.237.250:8082"
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	files := extractBundle(t, rr.Body.Bytes())
	dc := string(files["steamcmd/docker-compose.yml"])
	if !strings.Contains(dc, "cm2network/steamcmd") {
		t.Errorf("steamcmd compose missing image")
	}
	if !strings.Contains(dc, "dns:") {
		t.Errorf("steamcmd compose missing dns: field")
	}
	if !strings.Contains(dc, "CNCH_BASE_URL") {
		t.Errorf("steamcmd compose missing CNCH_BASE_URL env")
	}
}

// TestClientConfigBundle_UsesPublicBaseURL 验证 admin 配的 PublicBaseURL 优先于 r.Host。
//
// 场景：nginx 默认 proxy_set_header Host $proxy_host 让 r.Host 永远是 127.0.0.1:8082。
// 客户端从公网访问时，拿不到真 IP 写进配置。
// 修法：admin 在 SettingsView 配 "公开 Base URL"，生成配置时优先用。
func TestClientConfigBundle_UsesPublicBaseURL(t *testing.T) {
	fdb := newFakeAuthDB(t)
	_, _ = fdb.CreateUser(context.Background(), "admin", "admin1234", true)
	publicURL := "http://117.55.237.250"
	h := NewRouter(Options{
		AuthDB: fdb.DB,
		ListRegistries: func(ctx context.Context) ([]storage.Registry, error) {
			return []storage.Registry{
				{ID: 1, Name: "dockerhub", UpstreamURL: "https://registry-1.docker.io", MirrorPath: "/v2", Enabled: true},
			}, nil
		},
		GetSettings: func(ctx context.Context) (SystemSettings, error) { return SystemSettings{}, nil },
		PublicBaseURL: func() string { return publicURL },
	})

	// 直接打 /api/client-config/bundle（公开端点）
	req := httptest.NewRequest("POST", "/api/client-config/bundle", nil)
	req.RemoteAddr = "1.2.3.4:1234"
	// 不传 Host header，模拟从公网访问：后端 r.Host 是 "example.com" 之类
	req.Host = "example.com"
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	zipBytes := rec.Body.Bytes()
	zr, err := zip.NewReader(bytes.NewReader(zipBytes), int64(len(zipBytes)))
	if err != nil {
		t.Fatalf("zip read: %v", err)
	}

	// 抽取所有文本文件，验证不出现 127.0.0.1:8082 / r.Host (example.com)
	want := publicURL
	notWant := []string{"127.0.0.1:8082", "example.com"}
	for _, f := range zr.File {
		rc, _ := f.Open()
		content, _ := io.ReadAll(rc)
		_ = rc.Close()
		text := string(content)
		for _, bad := range notWant {
			if strings.Contains(text, bad) {
				t.Errorf("file %s contains %q (should use PublicBaseURL %q):\n%s", f.Name, bad, want, text)
			}
		}
		if !strings.Contains(text, want) {
			t.Errorf("file %s missing PublicBaseURL %q", f.Name, want)
		}
	}
}

// TestDaemonJSON_UsesPublicBaseURL 验证 /api/docker/daemon.json 端点也用 PublicBaseURL。
func TestDaemonJSON_UsesPublicBaseURL(t *testing.T) {
	fdb := newFakeAuthDB(t)
	u, _ := fdb.CreateUser(context.Background(), "admin", "admin1234", true)
	sess, _ := fdb.CreateSession(context.Background(), u.ID, "127.0.0.1", "test", SessionTTL)
	publicURL := "http://117.55.237.250"
	h := NewRouter(Options{
		AuthDB: fdb.DB,
		GetUpstreams: func(ctx context.Context) ([]Upstream, error) {
			return []Upstream{
				{ID: 1, Name: "dockerhub", UpstreamURL: "https://registry-1.docker.io", MirrorPath: "/v2", Enabled: true},
			}, nil
		},
		PublicBaseURL: func() string { return publicURL },
	})

	req := httptest.NewRequest("GET", "/api/docker/daemon.json", nil)
	req.Host = "example.com"
	req.AddCookie(&http.Cookie{Name: SessionCookieName, Value: sess.Token})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, publicURL) {
		t.Errorf("daemon.json missing PublicBaseURL %q:\n%s", publicURL, body)
	}
	if strings.Contains(body, "example.com") {
		t.Errorf("daemon.json leaked r.Host (example.com):\n%s", body)
	}
	if strings.Contains(body, "127.0.0.1:8082") {
		t.Errorf("daemon.json contains internal 127.0.0.1:8082:\n%s", body)
	}
}
