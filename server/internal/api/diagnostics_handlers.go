package api

import (
	"net/http"
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
