package api

import (
	"fmt"
	"net/http"
	"time"

	"github.com/cncachehub/server/internal/diagnostics"
)

// diagnosticsRunHandler GET /api/diagnostics/run
// 需要 admin。返回 3 个剧本的报告。
func diagnosticsRunHandler(opts Options) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !requireAdmin(opts, w, r) {
			return
		}
		fr := opts.RunDiagnostics(r.Context())
		writeJSON(w, http.StatusOK, fr)
	}
}

// diagnosticsBundleHandler POST /api/diagnostics/bundle
// 需要 admin。流式返回 .tar.gz。
//
// 设计：handler 自己设 Content-Disposition + Content-Type，jsonContentTypeMiddleware
// 默认设的 application/json 会被 handler 的 Set 覆盖（Go http.Header 延迟到 WriteHeader 前
// 都可改），然后把 w 透传给 diagnostics.WriteBundle（gzip + tar）。
//
// 单条文件写入失败不影响整个 bundle（WriteBundle 内部用 _ = add(...) 吞错）。
func diagnosticsBundleHandler(opts Options) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !requireAdmin(opts, w, r) {
			return
		}
		if opts.BundleSource.DB == nil {
			// 防御：DB 没注入（开发模式），直接 503
			writeError(w, http.StatusServiceUnavailable, "bundle_unavailable", "diagnostics bundle requires DB (server running in dev mode?)")
			return
		}
		filename := fmt.Sprintf("cncachehub-diagnostics-%s.tar.gz", time.Now().Format("20060102-150405"))
		w.Header().Set("Content-Type", "application/gzip")
		w.Header().Set("Content-Disposition", `attachment; filename="`+filename+`"`)
		w.WriteHeader(http.StatusOK)
		// 流式给客户端 —— handler 直接传 w，diagnostics.WriteBundle 内部包 gzip+tar。
		_ = diagnostics.WriteBundle(r.Context(), w, opts.BundleSource)
	}
}
