// Package proxy: upstream HTTP client.
package proxy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Upstream 封装对上游 Registry 的 HTTP 客户端。
//
// 设计：
//   - 单 http.Client（启用连接池复用 TCP）；
//   - 不读 body，直接 io.Copy 给客户端，节省内存；
//   - 透传上游 status / headers（剔除 hop-by-hop 与代理敏感头）；
//   - 区分网络错误和上游错误，便于归一（rate limit / 404 / 401 等）；
//   - 自动处理 Www-Authenticate token dance（Docker Hub 公开镜像匿名也要 token）。
type Upstream struct {
	baseURL string // 例如 "https://registry-1.docker.io"
	hc      *http.Client
	ua      string

	// tokenCache 缓存 (service, scope) → token；过期自动失效。
	tokenCache   map[string]tokenCacheEntry
	tokenCacheMu sync.Mutex
}

type tokenCacheEntry struct {
	Token     string
	ExpiresAt time.Time
}

// UpstreamOptions 构造配置。
type UpstreamOptions struct {
	BaseURL string
	Timeout time.Duration
	// UA 自定义 User-Agent（用于上游日志归因）。
	UA string
}

// NewUpstream 构造 Upstream。
func NewUpstream(opts UpstreamOptions) (*Upstream, error) {
	if opts.BaseURL == "" {
		return nil, errors.New("proxy: upstream base URL is required")
	}
	u, err := url.Parse(opts.BaseURL)
	if err != nil {
		return nil, fmt.Errorf("proxy: parse base URL: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, fmt.Errorf("proxy: upstream scheme must be http(s), got %q", u.Scheme)
	}
	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = 60 * time.Second
	}
	ua := opts.UA
	if ua == "" {
		ua = "cncachehub/dev"
	}
	hc := &http.Client{
		Timeout: timeout,
		Transport: &http.Transport{
			Proxy:                 http.ProxyFromEnvironment,
			DialContext:           (&net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
			MaxIdleConns:          50,
			MaxIdleConnsPerHost:   10,
			IdleConnTimeout:       90 * time.Second,
			TLSHandshakeTimeout:   10 * time.Second,
			ExpectContinueTimeout: 1 * time.Second,
		},
	}
	return &Upstream{
		baseURL: strings.TrimRight(opts.BaseURL, "/"),
		hc:      hc,
		ua:      ua,
	}, nil
}

// RoundTrip 发起上游请求并把响应头 + body 写到 w。
//
// 自动处理 Www-Authenticate token dance：
//   - 如果上游返回 401 + Www-Authenticate 头，自动解析 realm/service/scope，
//     去 auth server 拿 token，重试一次；
//   - 重试仍失败则原样回传上游响应。
func (u *Upstream) RoundTrip(ctx context.Context, w http.ResponseWriter, method, path string, reqHeader http.Header) (int, int64, error) {
	// 第一次请求（不带 Authorization）
	resp, err := u.doRaw(ctx, method, path, reqHeader, "")
	if err != nil {
		writeUpstreamError(w, "upstream request failed: "+err.Error())
		return http.StatusBadGateway, 0, err
	}
	defer resp.Body.Close()

	// 不需要 token dance：原样转发
	if resp.StatusCode != http.StatusUnauthorized || resp.Header.Get("Www-Authenticate") == "" {
		return u.forward(w, resp)
	}

	// 解析 Www-Authenticate 头拿 realm/service/scope
	wwwAuth := resp.Header.Get("Www-Authenticate")
	realm, service, scope, perr := parseWwwAuthenticate(wwwAuth)
	if perr != nil {
		// 解析失败：原样回 401
		return u.forward(w, resp)
	}
	// 关闭第一次 resp.Body（已读完头）
	_ = resp.Body.Close()

	// 拿 token
	token, terr := u.fetchToken(ctx, realm, service, scope)
	if terr != nil {
		writeUpstreamError(w, "fetch token: "+terr.Error())
		return http.StatusBadGateway, 0, terr
	}

	// 重试
	resp2, err := u.doRaw(ctx, method, path, reqHeader, token)
	if err != nil {
		writeUpstreamError(w, "upstream retry: "+err.Error())
		return http.StatusBadGateway, 0, err
	}
	defer resp2.Body.Close()
	return u.forward(w, resp2)
}

// doRaw 发起单次 HTTP 请求（不处理 401 重试）。
//
// authToken 非空时设 Authorization: Bearer。
func (u *Upstream) doRaw(ctx context.Context, method, path string, reqHeader http.Header, authToken string) (*http.Response, error) {
	fullURL := u.baseURL + path
	req, err := http.NewRequestWithContext(ctx, method, fullURL, nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	copyRequestHeaders(req.Header, reqHeader)
	req.Header.Set("User-Agent", u.ua)
	if authToken != "" {
		req.Header.Set("Authorization", "Bearer "+authToken)
	}
	req.Host = ""
	return u.hc.Do(req)
}

// forward 复制响应头 + body 到 w，跳过 hop-by-hop 和 Date/Server。
func (u *Upstream) forward(w http.ResponseWriter, resp *http.Response) (int, int64, error) {
	for k, vs := range resp.Header {
		if isHopByHopHeader(k) {
			continue
		}
		if k == "Date" || k == "Server" {
			continue
		}
		for _, v := range vs {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(resp.StatusCode)
	n, copyErr := io.Copy(w, resp.Body)
	if copyErr != nil {
		return resp.StatusCode, n, fmt.Errorf("upstream copy body: %w", copyErr)
	}
	return resp.StatusCode, n, nil
}

// parseWwwAuthenticate 解析 `Bearer realm="...",service="...",scope="..."`。
//
// 容错：缺字段时返回 error。
func parseWwwAuthenticate(h string) (realm, service, scope string, err error) {
	if !strings.HasPrefix(h, "Bearer ") {
		return "", "", "", fmt.Errorf("not a bearer challenge: %q", h)
	}
	h = strings.TrimPrefix(h, "Bearer ")
	// 形如 realm="...",service="...",scope="..."
	parts := strings.Split(h, ",")
	for _, p := range parts {
		p = strings.TrimSpace(p)
		eq := strings.Index(p, "=")
		if eq < 0 {
			continue
		}
		k := strings.TrimSpace(p[:eq])
		v := strings.Trim(p[eq+1:], `"`)
		switch k {
		case "realm":
			realm = v
		case "service":
			service = v
		case "scope":
			scope = v
		}
	}
	if realm == "" {
		return "", "", "", errors.New("realm missing in Www-Authenticate")
	}
	return realm, service, scope, nil
}

// tokenResponse 是 auth server 返回的 JSON。
type tokenResponse struct {
	Token       string `json:"token"`
	AccessToken string `json:"access_token"`
	// Docker Hub 用 "token"，有些 registry 用 "access_token"
	ExpiresIn int `json:"expires_in"`
}

// fetchToken 拿一个 registry bearer token。
//
// 缓存：相同 (service, scope) 复用 token，直到 expires_in 提前 30s 失效。
func (u *Upstream) fetchToken(ctx context.Context, realm, service, scope string) (string, error) {
	cacheKey := service + "\x00" + scope

	// 查缓存
	u.tokenCacheMu.Lock()
	if e, ok := u.tokenCache[cacheKey]; ok && time.Now().Before(e.ExpiresAt) {
		u.tokenCacheMu.Unlock()
		return e.Token, nil
	}
	u.tokenCacheMu.Unlock()

	// 拼 URL
	u2, err := url.Parse(realm)
	if err != nil {
		return "", fmt.Errorf("parse realm: %w", err)
	}
	q := u2.Query()
	if service != "" {
		q.Set("service", service)
	}
	if scope != "" {
		q.Set("scope", scope)
	}
	u2.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u2.String(), nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", u.ua)
	resp, err := u.hc.Do(req)
	if err != nil {
		return "", fmt.Errorf("token request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("token request status %d", resp.StatusCode)
	}
	var tr tokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&tr); err != nil {
		return "", fmt.Errorf("decode token: %w", err)
	}
	if tr.Token == "" {
		tr.Token = tr.AccessToken
	}
	if tr.Token == "" {
		return "", errors.New("token empty in response")
	}

	// 写缓存
	expiresIn := tr.ExpiresIn
	if expiresIn <= 0 {
		expiresIn = 60 // fallback
	}
	u.tokenCacheMu.Lock()
	if u.tokenCache == nil {
		u.tokenCache = make(map[string]tokenCacheEntry)
	}
	u.tokenCache[cacheKey] = tokenCacheEntry{
		Token:     tr.Token,
		ExpiresAt: time.Now().Add(time.Duration(expiresIn-30) * time.Second),
	}
	u.tokenCacheMu.Unlock()

	return tr.Token, nil
}

// isHopByHopHeader 判断 header 是否为 hop-by-hop（独立函数，Upstream + proxy 共用）。
func isHopByHopHeader(k string) bool {
	switch strings.ToLower(k) {
	case "connection", "keep-alive", "proxy-authenticate", "proxy-authorization",
		"te", "trailer", "transfer-encoding", "upgrade":
		return true
	}
	return false
}

// 兜底：导入 strconv 防 unused。
var _ = strconv.Itoa

// copyRequestHeaders 从客户端头复制到上游请求头，跳过 hop-by-hop 与代理敏感头。
func copyRequestHeaders(dst, src http.Header) {
	// 黑名单：hop-by-hop 头 + 不应透传给上游的头。
	skip := map[string]struct{}{
		"Connection":          {},
		"Keep-Alive":          {},
		"Proxy-Authenticate":  {},
		"Proxy-Authorization": {},
		"Te":                  {},
		"Trailer":             {},
		"Transfer-Encoding":   {},
		"Upgrade":             {},
		"Host":                {},
		"Authorization":       {}, // MVP 不做 token dance；私有镜像留 Phase 1.1
		"Cookie":              {},
	}
	for k, vs := range src {
		if _, bad := skip[http.CanonicalHeaderKey(k)]; bad {
			continue
		}
		// 跳过 X-Request-Id 等内部调试头
		if strings.HasPrefix(strings.ToLower(k), "x-cnch-") {
			continue
		}
		for _, v := range vs {
			dst.Add(k, v)
		}
	}
}

// copyResponseHeaders 从上游响应头复制到 w，跳过 hop-by-hop + Date/Server 等。
func copyResponseHeaders(dst, src http.Header) {
	skip := map[string]struct{}{
		"Connection":         {},
		"Keep-Alive":         {},
		"Proxy-Authenticate": {},
		"Proxy-Authorization": {},
		"Te":                 {},
		"Trailer":            {},
		"Transfer-Encoding":  {},
		"Upgrade":            {},
		// 上游的 Date / Server 头让 nginx 或前端补，避免暴露上游身份
		"Date":   {},
		"Server": {},
	}
	for k, vs := range src {
		if _, bad := skip[http.CanonicalHeaderKey(k)]; bad {
			continue
		}
		for _, v := range vs {
			dst.Add(k, v)
		}
	}
}

// writeUpstreamError 在 w 还能写时输出 502 + JSON。
func writeUpstreamError(w http.ResponseWriter, msg string) {
	// 这里不能直接 writeJSON（避免 import 循环），简单写。
	// 调用方应保证这个函数只在 round trip 启动前调用。
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusBadGateway)
	// 写一个最简的 JSON 错误体（手写避免依赖）。
	_, _ = io.WriteString(w, `{"error":{"code":"upstream_error","message":"`+escapeJSON(msg)+`"}}`)
}

func escapeJSON(s string) string {
	// 简单替换：双引号和反斜杠。
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	return s
}
