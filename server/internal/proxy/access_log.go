// Package proxy: access log entry.
package proxy

import "github.com/cncachehub/server/internal/cache"

// AccessLog 一次请求的可观测数据，由 Proxy 异步发给 main 的 writer。
type AccessLog struct {
	Method       string
	Path         string
	Status       int
	DurationMs   int64
	Cached       bool
	Bypassed     cache.BypassReason
	BypassReason string
	ClientIP     string
	Bytes        int64
	Error        string
}
