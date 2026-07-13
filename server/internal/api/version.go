package api

import (
	"net/http"
	"runtime"
)

// versionHandler 报告构建信息。
//
// 默认值：name="cncachehub", version="dev", go=<runtime.Version()>, commit="local"。
// 编译期可通过 -ldflags "-X main.version=... -X main.commit=..." 覆盖；
// 本包不直接读取 main 的变量，而是通过 Options.Build 注入，便于测试。
func versionHandler(b BuildInfo) http.HandlerFunc {
	// 兜底默认值，避免空字符串。
	if b.Name == "" {
		b.Name = "cncachehub"
	}
	if b.Version == "" {
		b.Version = "dev"
	}
	if b.Go == "" {
		b.Go = runtime.Version()
	}
	if b.Commit == "" {
		b.Commit = "local"
	}
	return func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{
			"name":    b.Name,
			"version": b.Version,
			"go":      b.Go,
			"commit":  b.Commit,
		})
	}
}
