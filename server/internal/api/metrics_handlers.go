package api

import (
	"net/http"

	"github.com/cncachehub/server/internal/metrics"
)

// metricsHandler GET /metrics — Prometheus 文本格式
// 公开（不需要 admin，遵循 Prometheus scrape 习惯）。
func metricsHandler(opts Options) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		handler := metrics.Handler(metrics.Source{
			DB:        opts.MetricsDB,
			DNSServer: opts.MetricsDNSServer,
			Upstreams: opts.MetricsUpstreams,
			Version:   opts.MetricsVersion,
			Commit:    opts.MetricsCommit,
			StartTime: opts.MetricsStartTime,
		})
		handler(w, r)
	}
}
