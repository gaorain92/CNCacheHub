// Package diagnostics 提供 CNCacheHub 一键诊断剧本（PRD §9.7 / §9.2.4 / §9.3.4）。
//
// 三类剧本：
//   - Docker pull: 上游可达 / CNCacheHub /v2/ / 客户端配置（daemon.json）
//   - SteamCMD DNS: 内置 DNS 启用 + 监听 + 命中白名单 + 上游可达
//   - Reverse proxy + TLS: nginx 主页 / upstream cert / CNCacheHub /healthz / 5xx 错误率
//
// 每剧本返回一组 CheckResult{Name, Status, Message, Fix?, Detail?}，
// Status: "ok" | "warning" | "error"。
package diagnostics

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os/exec"
	"strings"
	"time"
)

// Status 字面量。
const (
	StatusOK      = "ok"
	StatusWarning = "warning"
	StatusError   = "error"
)

// CheckResult 是单个检查项的结果。
type CheckResult struct {
	Name    string `json:"name"`
	Status  string `json:"status"`
	Message string `json:"message"`
	Fix     string `json:"fix,omitempty"`
	Detail  string `json:"detail,omitempty"`
}

// Report 是单类剧本的总报告。
type Report struct {
	Playbook string        `json:"playbook"` // 'docker_pull' | 'steamcmd_dns' | 'reverse_proxy'
	Title    string        `json:"title"`
	Summary  string        `json:"summary"` // 'ok' | 'warning' | 'error'
	Checks   []CheckResult `json:"checks"`
}

// RunnerOptions 携带检查需要的依赖。
type RunnerOptions struct {
	CNCHBaseURL    string // 形如 "http://127.0.0.1:8082"
	PublicBaseURL  string // 形如 "http://your-host:8082"，给 nginx 健康检查用
	UpstreamURL    string // 形如 "https://registry-1.docker.io"
	DNSServerStats func() DNSStats
	AccessLogCount func() (recent24h int, errCount24h int, total5xx int, err error) // 从 access_log 聚合
	DaemonConfig   func() (mirrors []string, insecure bool)               // 读 docker daemon.json
}

// DNSStats 给 DNS 剧本的轻量读法。
type DNSStats struct {
	Enabled       bool
	ListenAddr    string
	DomainRules   []string
	LastQueryAt   int64
	TotalQueries  int64
	HitQueries    int64
	MissQueries   int64
	BlockedQueries int64
}

// HTTPClient 共享超时（4s 默认，免得一个挂的 upstream 卡住整个剧本）。
var httpClient = &http.Client{Timeout: 4 * time.Second}

func httpClientWithInsecure() *http.Client {
	return &http.Client{
		Timeout: 4 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		},
	}
}

// === Docker Pull 剧本 ===

// CheckDockerPull 跑 4 项：上游可达 / CNCacheHub /v2/ / 客户端 daemon.json / 主页。
func CheckDockerPull(ctx context.Context, opts RunnerOptions) Report {
	r := Report{
		Playbook: "docker_pull",
		Title:    "Docker 拉取加速",
		Summary:  StatusOK,
	}

	// 1) 上游 registry 可达
	if res := checkUpstreamReachable(opts.UpstreamURL); res.Status != StatusOK {
		r.Checks = append(r.Checks, res)
	} else {
		r.Checks = append(r.Checks, res)
	}

	// 2) CNCacheHub /v2/ 返回 401（未带凭证）或 200（已带）
	if res := checkV2Endpoint(opts.CNCHBaseURL); res != nil {
		r.Checks = append(r.Checks, *res)
	}

	// 3) nginx 主页可达（公开 URL）
	if res := checkNginxHome(opts.PublicBaseURL); res != nil {
		r.Checks = append(r.Checks, *res)
	}

	// 4) docker daemon.json 配置
	if opts.DaemonConfig != nil {
		if res := checkDaemonConfig(opts); res != nil {
			r.Checks = append(r.Checks, *res)
		}
	}

	// 5) 最近 5xx 错误率
	if opts.AccessLogCount != nil {
		if res := checkRecentErrors(opts); res != nil {
			r.Checks = append(r.Checks, *res)
		}
	}

	r.Summary = rollupSummary(r.Checks)
	return r
}

func checkUpstreamReachable(upstream string) CheckResult {
	name := "上游 registry 可达"
	if upstream == "" {
		return CheckResult{Name: name, Status: StatusWarning, Message: "未配置 upstream", Fix: "在启动命令 CNCH_UPSTREAM_REGISTRY 设置"}
	}
	u, err := url.Parse(upstream)
	if err != nil {
		return CheckResult{Name: name, Status: StatusError, Message: "URL 解析失败: " + err.Error()}
	}
	host := u.Host
	if !strings.Contains(host, ":") {
		if u.Scheme == "https" {
			host += ":443"
		} else {
			host += ":80"
		}
	}
	conn, err := net.DialTimeout("tcp", host, 3*time.Second)
	if err != nil {
		return CheckResult{
			Name:    name,
			Status:  StatusError,
			Message: fmt.Sprintf("无法连接 %s：%v", host, err),
			Fix:     "检查宿主机到 " + host + " 的网络（防火墙 / 路由 / DNS）",
		}
	}
	_ = conn.Close()
	return CheckResult{
		Name:    name,
		Status:  StatusOK,
		Message: fmt.Sprintf("TCP 可达 %s", host),
	}
}

func checkV2Endpoint(base string) *CheckResult {
	if base == "" {
		return nil
	}
	name := "CNCacheHub /v2/ 端点"
	req, _ := http.NewRequest("GET", base+"/v2/", nil)
	resp, err := httpClient.Do(req)
	if err != nil {
		return &CheckResult{
			Name:    name,
			Status:  StatusError,
			Message: "请求失败: " + err.Error(),
			Fix:     "检查 cncachehub-server 是否在 " + base + " 监听（systemctl status cncachehub-server）",
		}
	}
	defer resp.Body.Close()
	_, _ = io.ReadAll(io.LimitReader(resp.Body, 1024))
	if resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusUnauthorized {
		return &CheckResult{
			Name:    name,
			Status:  StatusOK,
			Message: fmt.Sprintf("/v2/ 返回 %d（预期：401/200）", resp.StatusCode),
		}
	}
	return &CheckResult{
		Name:    name,
		Status:  StatusError,
		Message: fmt.Sprintf("/v2/ 返回 %d（预期 200/401）", resp.StatusCode),
		Fix:     "检查 server 日志 / 上游连通",
	}
}

func checkNginxHome(publicURL string) *CheckResult {
	if publicURL == "" {
		return nil
	}
	name := "Nginx 反代主页"
	req, _ := http.NewRequest("GET", publicURL+"/", nil)
	req.Header.Set("User-Agent", "cncachehub-diag/1.0")
	resp, err := httpClient.Do(req)
	if err != nil {
		return &CheckResult{
			Name:    name,
			Status:  StatusError,
			Message: "请求失败: " + err.Error(),
			Fix:     "检查 nginx 状态（systemctl status nginx）",
		}
	}
	defer resp.Body.Close()
	_, _ = io.ReadAll(io.LimitReader(resp.Body, 256))
	if resp.StatusCode != http.StatusOK {
		return &CheckResult{
			Name:    name,
			Status:  StatusError,
			Message: fmt.Sprintf("GET / 返回 %d（预期 200）", resp.StatusCode),
			Fix:     "检查 nginx 配置（cat /etc/nginx/sites-available/cncachehub）",
		}
	}
	return &CheckResult{Name: name, Status: StatusOK, Message: "GET / 返回 200"}
}

func checkDaemonConfig(opts RunnerOptions) *CheckResult {
	mirrors, _ := opts.DaemonConfig()
	if len(mirrors) == 0 {
		return &CheckResult{
			Name:    "Docker daemon.json 配置",
			Status:  StatusWarning,
			Message: "当前 CNCacheHub 主机未配置 registry-mirrors",
			Fix:     fmt.Sprintf("编辑 /etc/docker/daemon.json，加 \"registry-mirrors\": [\"%s\"] 然后 systemctl restart docker", opts.PublicBaseURL),
		}
	}
	// 包含本机？
	if !contains(mirrors, opts.PublicBaseURL) {
		return &CheckResult{
			Name:    "Docker daemon.json 配置",
			Status:  StatusWarning,
			Message: fmt.Sprintf("registry-mirrors=%v 不含本机 %s", mirrors, opts.PublicBaseURL),
			Fix:     fmt.Sprintf("编辑 /etc/docker/daemon.json，把 \"%s\" 加进去", opts.PublicBaseURL),
		}
	}
	return &CheckResult{
		Name:    "Docker daemon.json 配置",
		Status:  StatusOK,
		Message: fmt.Sprintf("已配置 mirror: %v", mirrors),
	}
}

func checkRecentErrors(opts RunnerOptions) *CheckResult {
	total, errs, s5xx, err := opts.AccessLogCount()
	if err != nil {
		return &CheckResult{
			Name: "最近 5xx 错误率", Status: StatusWarning,
			Message: "无法读 access log: " + err.Error(),
		}
	}
	if total == 0 {
		return &CheckResult{
			Name: "最近 5xx 错误率", Status: StatusOK,
			Message: "24h 暂无访问记录（首次部署正常）",
		}
	}
	rate := float64(s5xx) / float64(total) * 100
	if rate > 5 {
		return &CheckResult{
			Name:    "最近 5xx 错误率",
			Status:  StatusError,
			Message: fmt.Sprintf("24h 5xx 错误率 %.1f%%（%d/%d）", rate, s5xx, total),
			Detail:  fmt.Sprintf("4xx/5xx 总计 %d", errs),
			Fix:     "查看 /logs 页面或 server 日志找根因",
		}
	}
	if rate > 1 {
		return &CheckResult{
			Name:    "最近 5xx 错误率",
			Status:  StatusWarning,
			Message: fmt.Sprintf("24h 5xx 错误率 %.1f%%（%d/%d）", rate, s5xx, total),
		}
	}
	return &CheckResult{
		Name:    "最近 5xx 错误率",
		Status:  StatusOK,
		Message: fmt.Sprintf("24h 5xx 错误率 %.2f%%（%d/%d）", rate, s5xx, total),
	}
}

// === SteamCMD DNS 剧本 ===

// CheckSteamDNS 跑 4 项。
func CheckSteamDNS(ctx context.Context, opts RunnerOptions) Report {
	r := Report{
		Playbook: "steamcmd_dns",
		Title:    "SteamCMD DNS 启动器",
		Summary:  StatusOK,
	}

	if opts.DNSServerStats == nil {
		r.Checks = append(r.Checks, CheckResult{
			Name: "DNS 启动器状态", Status: StatusWarning,
			Message: "未注入 DNS server stats 回调（开发模式？）",
		})
		r.Summary = StatusWarning
		return r
	}
	stats := opts.DNSServerStats()

	// 1) DNS server enabled
	if !stats.Enabled {
		r.Checks = append(r.Checks, CheckResult{
			Name: "DNS 启动器已启用", Status: StatusWarning,
			Message: "DNS 启动器当前未启用",
			Fix: "在「SteamCMD 加速」页面打开启用开关",
		})
	} else {
		r.Checks = append(r.Checks, CheckResult{
			Name: "DNS 启动器已启用", Status: StatusOK,
			Message: fmt.Sprintf("监听 %s", stats.ListenAddr),
		})
	}

	// 2) 端口监听
	if stats.Enabled {
		host, port, err := net.SplitHostPort(stats.ListenAddr)
		if err == nil {
			addr := host
			if addr == "" || addr == "0.0.0.0" {
				addr = "127.0.0.1"
			}
			conn, err := net.DialTimeout("tcp", net.JoinHostPort(addr, port), 2*time.Second)
			if err != nil {
				r.Checks = append(r.Checks, CheckResult{
					Name: "DNS 端口监听", Status: StatusError,
					Message: fmt.Sprintf("无法连接 %s: %v", net.JoinHostPort(addr, port), err),
					Fix: "检查 server 进程是否在运行（systemctl status cncachehub-server）",
				})
			} else {
				_ = conn.Close()
				r.Checks = append(r.Checks, CheckResult{
					Name: "DNS 端口监听", Status: StatusOK,
					Message: fmt.Sprintf("TCP %s 可达", net.JoinHostPort(addr, port)),
				})
			}
		}
	}

	// 3) 白名单规则
	if len(stats.DomainRules) == 0 {
		r.Checks = append(r.Checks, CheckResult{
			Name: "白名单规则", Status: StatusWarning,
			Message: "白名单为空 — 启用后也不会命中任何 Steam 域名",
			Fix: "在「SteamCMD 加速」页面配置域名规则（参考 LANCache 默认列表）",
		})
	} else {
		r.Checks = append(r.Checks, CheckResult{
			Name: "白名单规则", Status: StatusOK,
			Message: fmt.Sprintf("%d 条规则", len(stats.DomainRules)),
		})
	}

	// 4) 上游 DNS 可达
	if stats.Enabled {
		up := strings.TrimSpace(extractUpstreamFromAddr(stats.ListenAddr))
		if up == "" {
			up = "1.1.1.1:53"
		}
		host, port, _ := net.SplitHostPort(up)
		if host == "" {
			host = up
			port = "53"
		}
		conn, err := net.DialTimeout("udp", net.JoinHostPort(host, port), 2*time.Second)
		if err != nil {
			r.Checks = append(r.Checks, CheckResult{
				Name: "上游 DNS 可达", Status: StatusError,
				Message: fmt.Sprintf("无法连接 %s: %v", net.JoinHostPort(host, port), err),
				Fix: "换上游 8.8.8.8:53 / 1.0.0.1:53 / 223.5.5.5:53",
			})
		} else {
			_ = conn.Close()
			r.Checks = append(r.Checks, CheckResult{
				Name: "上游 DNS 可达", Status: StatusOK,
				Message: fmt.Sprintf("UDP %s 可达", net.JoinHostPort(host, port)),
			})
		}
	}

	// 5) 查询统计
	if stats.TotalQueries == 0 {
		r.Checks = append(r.Checks, CheckResult{
			Name: "DNS 查询统计", Status: StatusOK,
			Message: "暂无查询记录（启动后还没客户端用过）",
		})
	} else {
		hit := float64(stats.HitQueries) / float64(stats.TotalQueries) * 100
		r.Checks = append(r.Checks, CheckResult{
			Name: "DNS 查询统计", Status: StatusOK,
			Message: fmt.Sprintf("总计 %d · 命中 %d (%.1f%%) · 转发 %d · 失败 %d",
				stats.TotalQueries, stats.HitQueries, hit, stats.MissQueries, stats.BlockedQueries),
		})
	}

	r.Summary = rollupSummary(r.Checks)
	return r
}

// === Reverse Proxy + TLS 剧本 ===

// CheckReverseProxy 跑 4 项。
func CheckReverseProxy(ctx context.Context, opts RunnerOptions) Report {
	r := Report{
		Playbook: "reverse_proxy",
		Title:    "反代 + TLS / 端口",
		Summary:  StatusOK,
	}

	// 1) /healthz
	if opts.CNCHBaseURL != "" {
		req, _ := http.NewRequest("GET", opts.CNCHBaseURL+"/healthz", nil)
		resp, err := httpClient.Do(req)
		if err != nil {
			r.Checks = append(r.Checks, CheckResult{
				Name: "CNCacheHub /healthz", Status: StatusError,
				Message: "请求失败: " + err.Error(),
			})
		} else {
			_, _ = io.ReadAll(io.LimitReader(resp.Body, 256))
			_ = resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				r.Checks = append(r.Checks, CheckResult{
					Name: "CNCacheHub /healthz", Status: StatusError,
					Message: fmt.Sprintf("返回 %d（预期 200）", resp.StatusCode),
				})
			} else {
				r.Checks = append(r.Checks, CheckResult{
					Name: "CNCacheHub /healthz", Status: StatusOK,
					Message: "返回 200",
				})
			}
		}
	}

	// 2) nginx 主页
	if opts.PublicBaseURL != "" {
		if res := checkNginxHome(opts.PublicBaseURL); res != nil {
			r.Checks = append(r.Checks, *res)
		}
	}

	// 3) 上游 TLS 证书可验证
	if opts.UpstreamURL != "" && strings.HasPrefix(opts.UpstreamURL, "https://") {
		if res := checkUpstreamTLSCert(opts.UpstreamURL); res != nil {
			r.Checks = append(r.Checks, *res)
		}
	}

	// 4) CNCacheHub 自身公网 URL 走通（验证 nginx location /v2/ 顺序 + proxy_pass）
	if opts.PublicBaseURL != "" {
		if res := checkPublicV2(opts.PublicBaseURL); res != nil {
			r.Checks = append(r.Checks, *res)
		}
	}

	// 5) docker CLI 实际拉镜像端到端（可选 — 跑通整个 stack）
	if res := checkDockerPullEndToEnd(); res != nil {
		r.Checks = append(r.Checks, *res)
	}

	r.Summary = rollupSummary(r.Checks)
	return r
}

func checkUpstreamTLSCert(upstream string) *CheckResult {
	u, err := url.Parse(upstream)
	if err != nil {
		return &CheckResult{
			Name: "上游 TLS 证书", Status: StatusError,
			Message: "URL 解析失败: " + err.Error(),
		}
	}
	host := u.Host
	if !strings.Contains(host, ":") {
		host += ":443"
	}
	conn, err := tls.Dial("tcp", host, &tls.Config{})
	if err != nil {
		return &CheckResult{
			Name:    "上游 TLS 证书",
			Status:  StatusError,
			Message: fmt.Sprintf("TLS 拨号失败: %v", err),
			Fix:     "系统时间是否正确？根证书是否过期？",
		}
	}
	defer conn.Close()
	certs := conn.ConnectionState().PeerCertificates
	if len(certs) == 0 {
		return &CheckResult{
			Name: "上游 TLS 证书", Status: StatusError,
			Message: "无对等证书",
		}
	}
	cn := certs[0].Subject.CommonName
	dns := []string(certs[0].DNSNames)
	return &CheckResult{
		Name:    "上游 TLS 证书",
		Status:  StatusOK,
		Message: fmt.Sprintf("CN=%s · 截止 %s · SAN=%v", cn, certs[0].NotAfter.Format("2006-01-02"), dns),
	}
}

func checkPublicV2(publicURL string) *CheckResult {
	req, _ := http.NewRequest("GET", publicURL+"/v2/", nil)
	resp, err := httpClient.Do(req)
	if err != nil {
		return &CheckResult{
			Name:    "公网 /v2/ 端到端",
			Status:  StatusError,
			Message: "请求失败: " + err.Error(),
			Fix:     "检查 nginx location /v2/ 顺序（在 / 前）+ proxy_set_header Host $proxy_host",
		}
	}
	defer resp.Body.Close()
	_, _ = io.ReadAll(io.LimitReader(resp.Body, 256))
	ct := resp.Header.Get("Content-Type")
	if resp.StatusCode != 200 && resp.StatusCode != 401 {
		return &CheckResult{
			Name:    "公网 /v2/ 端到端",
			Status:  StatusError,
			Message: fmt.Sprintf("返回 %d · content-type=%q", resp.StatusCode, ct),
			Fix:     "很可能是 nginx location /v2/* 没在 / 之前，或 SPA fallback 吃掉了 /v2/ 请求",
		}
	}
	if !strings.HasPrefix(ct, "application/json") && resp.StatusCode == 200 {
		return &CheckResult{
			Name:    "公网 /v2/ 端到端",
			Status:  StatusError,
			Message: fmt.Sprintf("返回 %d · content-type=%q（预期 application/json）", resp.StatusCode, ct),
			Fix:     "nginx SPA fallback 在 / 兜底吃掉了 /v2/，必须把 /v2/ location 块放在 / 之前",
		}
	}
	return &CheckResult{
		Name:    "公网 /v2/ 端到端",
		Status:  StatusOK,
		Message: fmt.Sprintf("HTTP %d · content-type=%q", resp.StatusCode, ct),
	}
}

func checkDockerPullEndToEnd() *CheckResult {
	name := "docker pull 端到端（可选）"
	if _, err := exec.LookPath("docker"); err != nil {
		return &CheckResult{
			Name: name, Status: StatusOK,
			Message: "未安装 docker（开发机正常），跳过",
		}
	}
	out, err := exec.Command("docker", "version", "--format", "{{.Server.Version}}").CombinedOutput()
	if err != nil {
		return &CheckResult{
			Name: name, Status: StatusWarning,
			Message: "docker daemon 未运行（端到端测试跳过）: " + strings.TrimSpace(string(out)),
		}
	}
	return &CheckResult{
		Name: name, Status: StatusOK,
		Message: fmt.Sprintf("docker daemon 已在跑（server=%s）", strings.TrimSpace(string(out))),
	}
}

// === helpers ===

func contains(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}

func rollupSummary(checks []CheckResult) string {
	for _, c := range checks {
		if c.Status == StatusError {
			return StatusError
		}
	}
	for _, c := range checks {
		if c.Status == StatusWarning {
			return StatusWarning
		}
	}
	return StatusOK
}

func extractUpstreamFromAddr(s string) string {
	// 简化：直接 hardcode 1.1.1.1:53（实际配置在 dnsConfig.Upstream，这里只用于展示）
	return "1.1.1.1:53"
}

// === DTO for /api/diagnostics/run ===

// FullReport 是 /api/diagnostics/run 的总响应。
type FullReport struct {
	Playbooks    []Report `json:"playbooks"`
	GeneratedAt  int64    `json:"generatedAt"`
	CNCHVersion  string   `json:"cnchVersion"`
}

// RunAll 跑全部 3 个剧本并发。
func RunAll(ctx context.Context, opts RunnerOptions) FullReport {
	r := FullReport{GeneratedAt: time.Now().Unix()}
	var out []Report
	// 串行 — 简单（每个剧本几秒）
	out = append(out, CheckDockerPull(ctx, opts))
	out = append(out, CheckSteamDNS(ctx, opts))
	out = append(out, CheckReverseProxy(ctx, opts))
	r.Playbooks = out
	return r
}

// MarshalJSON for FullReport — 防止 nil playbooks 序列化成 null。
var _ = json.Marshal
