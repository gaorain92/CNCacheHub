// recoverer.go — 自定义 panic recover middleware。
//
// 跟 chimw.Recoverer 行为类似但多记一层 stack trace + panic value。
// chimw 默认只打一行 "PANIC" 不带 stack，线上排障几乎没用。
//
// 行为：
//   - 捕获 panic
//   - runtime.Stack(buf, false) 拿当前 goroutine 栈
//   - log.Error 写完整 stack（带 request_id + remote + path 便于关联）
//   - 返 500 internal_server_error JSON
package api

import (
	"net/http"
	"runtime"
	"runtime/debug"

	chimw "github.com/go-chi/chi/v5/middleware"

	logpkg "github.com/cncachehub/server/internal/log"
)

// recovererMiddleware 替换 chimw.Recoverer — 多打 stack + 友好错误响应。
func recovererMiddleware() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if rec := recover(); rec != nil {
					// 拿 4KB stack — 99% 的 panic 栈都装得下，又不会写爆日志
					stack := make([]byte, 4096)
					n := runtime.Stack(stack, false)
					stackStr := string(stack[:n])

					reqID := chimw.GetReqID(r.Context())
					logpkg.Error("panic recovered",
						"request_id", reqID,
						"method", r.Method,
						"path", r.URL.Path,
						"remote", r.RemoteAddr,
						"panic", rec,
						// debug.Stack() 返回完整 stack（包含所有 goroutine），便于诊断
						"all_stacks", string(debug.Stack()),
						// 当前 goroutine 的栈（短版）— 99% 情况定位就够了
						"stack", stackStr,
					)
					// 用户侧：友好 500（不暴露 panic 内容，避免信息泄露）
					writeError(w, http.StatusInternalServerError,
						"internal_error", "internal server error")
				}
			}()
			next.ServeHTTP(w, r)
		})
	}
}
