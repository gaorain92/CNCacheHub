// Package proxy: HuggingFace 镜像端点（HF_ENDPOINT 兼容）。
//
// 用途：让用户设 `HF_ENDPOINT=http://cnch:8082/hf` 之后，
// `huggingface_hub.snapshot_download(repo_id=...)` 等操作透明走我们的缓存。
//
// 路由：/hf/* — 二选一：
//
//   1. /hf/api/...               → 透传到 huggingface.co/api/...（tree / metadata / whoami）
//                                 不缓存（HF API 响应是 JSON 元数据，每次可能变）
//   2. /hf/{org}/{name}/resolve/{rev}/{file}
//                              → 改写到 /r/huggingface-models/{org}/{name}/resolve/{rev}/{file}
//                                 走现有 ResourceHandler：缓存 + token 注入 + Range 断点续传
//
// 其它路径（/hf/foo、/hf/{org}/{name}/tree/...）返 404。
//
// HF token 从 system_settings.huggingface_token 读（gated 模型需要）；
// API 透传时也注入 Authorization 头，HF 会按需返回 gated 文件元数据。
package proxy

import (
	"log/slog"
	"net/http"
	"net/http/httputil"
	"net/url"
	"regexp"
	"strings"
	"time"
)

// HFMirrorHandler 处理 /hf/* 路由。
//
// 访问控制不在这里做 — 由 main.go 在 router 层 wrap `access.Middleware`
// （和 /r/* /v2/* 一致），保持职责单一。
type HFMirrorHandler struct {
	// ResourceHandler 处理文件请求（缓存 + token + Range）。通常与 /r/* 共享。
	ResourceHandler http.Handler
	// GetHuggingFaceToken 返 HF access token（空串 = 不注入）。
	GetHuggingFaceToken func() string
	// Logger 可选；nil 用 slog.Default()。
	Logger *slog.Logger
}

// NewHFMirrorHandler 构造 mirror handler。
func NewHFMirrorHandler(resourceHandler http.Handler, getToken func() string, log *slog.Logger) *HFMirrorHandler {
	if log == nil {
		log = slog.Default()
	}
	return &HFMirrorHandler{
		ResourceHandler:     resourceHandler,
		GetHuggingFaceToken: getToken,
		Logger:              log,
	}
}

// HFMirrorPrefix 是公开的 URL 前缀，用户 HF_ENDPOINT = "<cnch>/hf"。
const HFMirrorPrefix = "/hf/"

// fileResolveRegex 匹配 /hf/{org}/{name}/resolve/{rev}/{file}。
//
// 例：
//   /hf/Qwen/Qwen2.5-1.5B-Instruct/resolve/main/config.json
//   → org=Qwen, name=Qwen2.5-1.5B-Instruct, rev=main, file=config.json
//
// {org}、{name} 不含 "/"，{rev} 不含 "/"（HF ref 规范），{file} 可含 "/"。
var fileResolveRegex = regexp.MustCompile(`^([^/]+)/([^/]+)/resolve/([^/]+)/(.+)$`)

// ServeHTTP 实现 /hf/* 路由分发。
func (h *HFMirrorHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// 1. 必须是 /hf 前缀（路由已经过滤；双保险）
	rest, ok := strings.CutPrefix(r.URL.Path, HFMirrorPrefix)
	if !ok {
		http.NotFound(w, r)
		return
	}

	// 1b. 防御路径穿越：拒绝任何含 `..` 段（任何位置、含 file 段）。
	//
	// 例：/hf/Qwen/Qwen/resolve/main/../../etc/passwd
	//   file 段是 "../../etc/passwd" → 拒绝。
	//
	// 不依赖下游（ResourceHandler / 上游）做 normalize — 我们在边界挡掉。
	// 单段 "../" 也挡（虽然 net/http 通常会 normalize，但显式检查无副作用）。
	if hasPathTraversal(rest) {
		h.Logger.Warn("hf mirror: path traversal rejected", "path", r.URL.Path)
		http.Error(w, "path traversal not allowed", http.StatusBadRequest)
		return
	}

	// 2. 分发：API 透传 vs 文件改写
	if strings.HasPrefix(rest, "api/") {
		h.serveAPIPassthrough(w, r, rest)
		return
	}
	if m := fileResolveRegex.FindStringSubmatch(rest); m != nil {
		h.serveFileRewrite(w, r, m)
		return
	}
	http.NotFound(w, r)
}

// hasPathTraversal 报告 path 任意段是否含 ".."。
//
// 严格匹配单段等于 ".."（不依赖 path 库，避免遗漏 edge case）。
func hasPathTraversal(path string) bool {
	for _, seg := range strings.Split(path, "/") {
		if seg == ".." {
			return true
		}
	}
	return false
}

// stripProxyHeaders 剥掉所有代理 / forwarding 头。
//
// 与 upstream.copyRequestHeaders 的 skip list 保持一致 — 集中维护稍嫌啰嗦，
// 但在 hf_mirror 单独写一份更直接（避免 package 循环依赖）。修改时记得同步。
func stripProxyHeaders(h http.Header) {
	// 一次 Set("") 即可删除（http.Header 的 Del 也是设空）
	for _, k := range []string{
		"X-Forwarded-For", "X-Forwarded-Proto", "X-Forwarded-Host",
		"X-Forwarded-Port", "X-Forwarded-Scheme",
		"X-Real-IP", "X-Real-Ip",
		"Forwarded",
		"Client-Ip",
		"Cf-Connecting-Ip", "True-Client-Ip", "Fastly-Client-Ip",
		"X-Client-Ip", "X-Original-Forwarded-For",
		// 不剥 Host（httputil.ReverseProxy 自己会处理）
		// 不剥 Authorization / Cookie（这里要 set Authorization）
	} {
		h.Del(k)
	}
}

// serveAPIPassthrough 把 /hf/api/... 透传到 huggingface.co/api/...
// （用 httputil.ReverseProxy 走标准反向代理语义：保留 headers、跟随 302、改写 Location 等）
func (h *HFMirrorHandler) serveAPIPassthrough(w http.ResponseWriter, r *http.Request, rest string) {
	target, err := url.Parse("https://huggingface.co")
	if err != nil {
		http.Error(w, "build target: "+err.Error(), http.StatusInternalServerError)
		return
	}
	proxy := httputil.NewSingleHostReverseProxy(target)
	originalDirector := proxy.Director
	proxy.Director = func(req *http.Request) {
		originalDirector(req)
		// 重写 path：去掉 /hf 前缀
		req.URL.Path = "/" + rest
		req.URL.RawPath = "" // 让 net/http 重新从 Path 编码
		req.Host = target.Host
		// 剥掉所有代理 / forwarding 头 — 跟 copyRequestHeaders 一致：
		// 不让攻击者通过 CNCacheHub 伪造 IP 给 huggingface.co。
		stripProxyHeaders(req.Header)
		// 注入 token
		if h.GetHuggingFaceToken != nil {
			if tok := strings.TrimSpace(h.GetHuggingFaceToken()); tok != "" {
				req.Header.Set("Authorization", "Bearer "+tok)
			}
		}
	}
	// 跟着 HF 的 30x（典型是 dataset 跳到具体版本，无关紧要）
	proxy.ModifyResponse = func(resp *http.Response) error {
		// 不修改 body；只记录 4xx/5xx 方便排查
		if resp.StatusCode >= 400 {
			h.Logger.Info("hf mirror api upstream error", "path", rest, "status", resp.StatusCode)
		}
		return nil
	}
	proxy.ErrorHandler = func(rw http.ResponseWriter, req *http.Request, err error) {
		h.Logger.Warn("hf mirror api upstream failed", "path", rest, "err", err)
		http.Error(rw, "upstream hf api failed: "+err.Error(), http.StatusBadGateway)
	}
	// 自定义 client：比默认 0 超时给个 30s 兜底
	if proxy.Transport == nil {
		proxy.Transport = &http.Transport{
			ResponseHeaderTimeout: 30 * time.Second,
		}
	}
	proxy.ServeHTTP(w, r)
}

// serveFileRewrite 改写 URL 到 /r/huggingface-models/{org}/{name}/resolve/{rev}/{file}
// 并把请求交给 ResourceHandler。复用现有缓存 + token + Range 逻辑。
func (h *HFMirrorHandler) serveFileRewrite(w http.ResponseWriter, r *http.Request, m []string) {
	org, name, rev, file := m[1], m[2], m[3], m[4]
	// 构造内部路径：/r/huggingface-models/<org>/<name>/resolve/<rev>/<file>
	// 注意：<file> 可能含 "/" 但不是 path 分隔符语义，必须保留原样。
	// 用 path.Join 自动处理，但 path.Join 会清理 ./ 和 ../ — 不可，要直接拼。
	internalPath := "/r/huggingface-models/" + org + "/" + name + "/resolve/" + rev + "/" + file
	// 复制 request 修改 path
	r2 := r.Clone(r.Context())
	r2.URL.Path = internalPath
	r2.URL.RawPath = "" // 让 ServeHTTP 重新从 Path 解码
	r2.RequestURI = ""   // 不能在 server-handled request 里保留
	if h.ResourceHandler == nil {
		http.Error(w, "resource handler not configured", http.StatusServiceUnavailable)
		return
	}
	h.ResourceHandler.ServeHTTP(w, r2)
}
