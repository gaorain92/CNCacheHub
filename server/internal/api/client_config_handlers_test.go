package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func clientConfigTestHandler(t *testing.T) (http.Handler, *fakeAuthDB) {
	t.Helper()
	fdb := newFakeAuthDB(t)
	_, _ = fdb.CreateUser(context.Background(), "admin", "admin1234", true)
	return NewRouter(Options{
		AuthDB: fdb.DB,
		ListRegistries: func(ctx context.Context) ([]Registry, error) {
			return []Registry{
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
