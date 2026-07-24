// Package metrics 提供 Prometheus 文本格式指标输出（PRD §15.5 P2#2）。
//
// 不引入 prometheus/client_golang 依赖：手写 minimal registry + text 格式输出。
// 保持二进制小 + 零外部 dep。
//
// 暴露的指标：
//   - cnch_uptime_seconds         (gauge)
//   - cnch_start_time_seconds     (gauge)
//   - cnch_cache_entries          (gauge)
//   - cnch_cache_bytes            (gauge)
//   - cnch_cache_hits_total       (gauge, sum of cache_entries.hit_count + preheat_items.hit_count)
//   - cnch_cache_misses_24h       (gauge, request_logs 24h status=200 cached=0)
//   - cnch_cache_bypassed_24h     (gauge, request_logs 24h bypassed=1)
//   - cnch_request_count_24h      (gauge)
//   - cnch_errors_24h             (gauge, status >= 400)
//   - cnch_bytes_out_24h          (gauge)
//   - cnch_dns_queries_total      (gauge, from dns server stats)
//   - cnch_dns_hits_total         (gauge)
//   - cnch_dns_misses_total       (gauge)
//   - cnch_resource_rules         (gauge)
//   - cnch_resource_cache_entries (gauge)
//   - cnch_active_upstreams       (gauge)
//   - cnch_upstream_reachable{upstream="..."} (gauge, 0/1)
//   - cnch_build_info{version,commit} (gauge, 永远 1)
package metrics

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	dnsserver "github.com/cncachehub/server/internal/dns"
	"github.com/cncachehub/server/internal/storage"
)

// Source 注入渲染 /metrics 所需的所有数据源。
type Source struct {
	DB          DBStats
	DNSServer   DNSStats
	Upstreams   func() []UpstreamStatus // upstream 健康快照
	Version     string
	Commit      string
	StartTime   time.Time
}

// DBStats 是 metrics 对 storage.DB 的最小读接口（避免循环 import）。
type DBStats interface {
	DashboardSummary(ctx context.Context) (storage.DashboardSummary, error)
	ListResourceRules(ctx context.Context) ([]storage.ResourceRule, error)
	ResourceStats(ctx context.Context) (storage.ResourceStatsSummary, error)
}

// DNSStats 是 metrics 对 dnsserver.Server 的最小读接口。
type DNSStats interface {
	Stats() dnsserver.Stats
}

// UpstreamStatus 是单条 upstream 健康快照。
type UpstreamStatus struct {
	Name      string
	URL       string
	Reachable bool
}

// Handler 返回 /metrics 路由的 http.Handler。
func Handler(src Source) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// 公开端点（不需 admin）— Prometheus scrape 习惯
		metrics := collect(r.Context(), src)
		w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_ = writeMetrics(w, metrics)
	}
}

// Metric 是一条指标。
type Metric struct {
	Name   string
	Help   string
	Type   string // "gauge" | "counter"
	Value  float64
	Labels map[string]string
}

func collect(ctx context.Context, src Source) []Metric {
	var out []Metric
	now := time.Now()
	uptime := now.Sub(src.StartTime).Seconds()

	out = append(out, Metric{Name: "cnch_uptime_seconds", Type: "gauge", Help: "Server uptime in seconds.", Value: uptime})
	out = append(out, Metric{Name: "cnch_start_time_seconds", Type: "gauge", Help: "Unix time of server start.", Value: float64(src.StartTime.Unix())})
	out = append(out, Metric{Name: "cnch_build_info", Type: "gauge", Help: "Build info (always 1).", Value: 1, Labels: map[string]string{"version": src.Version, "commit": src.Commit}})

	if src.DB == nil {
		return out
	}

	// 缓存
	ds, err := src.DB.DashboardSummary(ctx)
	if err == nil {
		out = append(out, Metric{Name: "cnch_cache_entries", Type: "gauge", Help: "Number of cache entries on disk.", Value: float64(ds.CacheEntries)})
		out = append(out, Metric{Name: "cnch_cache_bytes", Type: "gauge", Help: "Total bytes used by cache entries.", Value: float64(ds.CacheBytes)})
		out = append(out, Metric{Name: "cnch_cache_hits_total", Type: "gauge", Help: "Sum of cache_entries.hit_count.", Value: float64(ds.CacheHits)})
		out = append(out, Metric{Name: "cnch_cache_bypassed_24h", Type: "gauge", Help: "Bypassed entries count over 24h.", Value: float64(ds.BypassedCount)})
		out = append(out, Metric{Name: "cnch_request_count_24h", Type: "gauge", Help: "Total requests in last 24h.", Value: float64(ds.RequestCount24h)})
		out = append(out, Metric{Name: "cnch_errors_24h", Type: "gauge", Help: "Total 4xx/5xx responses in last 24h.", Value: float64(ds.ErrorCount24h)})
		out = append(out, Metric{Name: "cnch_bytes_out_24h", Type: "gauge", Help: "Total bytes served in last 24h.", Value: float64(ds.BytesOut24h)})
		out = append(out, Metric{Name: "cnch_cache_misses_24h", Type: "gauge", Help: "200 responses with cached=0 in last 24h.", Value: float64(ds.MissCount)})
		out = append(out, Metric{Name: "cnch_active_upstreams", Type: "gauge", Help: "Enabled registry upstreams.", Value: float64(ds.ActiveUpstreams)})
	}

	// 资源加速
	if rules, err := src.DB.ListResourceRules(ctx); err == nil {
		var enabled int
		for _, r := range rules {
			if r.Enabled {
				enabled++
			}
		}
		out = append(out, Metric{Name: "cnch_resource_rules", Type: "gauge", Help: "Total resource rules (enabled+disabled).", Value: float64(len(rules))})
		out = append(out, Metric{Name: "cnch_resource_rules_enabled", Type: "gauge", Help: "Enabled resource rules.", Value: float64(enabled)})
	}
	if rcs, err := src.DB.ResourceStats(ctx); err == nil {
		out = append(out, Metric{Name: "cnch_resource_cache_entries", Type: "gauge", Help: "Total resource cache entries.", Value: float64(rcs.Total)})
		out = append(out, Metric{Name: "cnch_resource_cache_bytes", Type: "gauge", Help: "Total bytes cached by resource rules.", Value: float64(rcs.TotalBytes)})
	}

	// DNS
	if src.DNSServer != nil {
		stats := src.DNSServer.Stats()
		out = append(out, Metric{Name: "cnch_dns_queries_total", Type: "gauge", Help: "Total DNS queries since server start.", Value: float64(stats.TotalQueries)})
		out = append(out, Metric{Name: "cnch_dns_hits_total", Type: "gauge", Help: "DNS queries matched by whitelist.", Value: float64(stats.HitQueries)})
		out = append(out, Metric{Name: "cnch_dns_misses_total", Type: "gauge", Help: "DNS queries forwarded upstream.", Value: float64(stats.MissQueries)})
		out = append(out, Metric{Name: "cnch_dns_blocked_total", Type: "gauge", Help: "DNS queries that errored.", Value: float64(stats.BlockedQueries)})
	}

	// Upstreams
	if src.Upstreams != nil {
		for _, u := range src.Upstreams() {
			v := 0.0
			if u.Reachable {
				v = 1
			}
			out = append(out, Metric{
				Name: "cnch_upstream_reachable", Type: "gauge",
				Help: "1 if upstream reachable, 0 otherwise.",
				Value: v,
				Labels: map[string]string{"upstream": u.Name, "url": u.URL},
			})
		}
	}

	return out
}

func writeMetrics(w io.Writer, ms []Metric) error {
	// 按 name 分组输出（Prometheus 期望）
	sort.Slice(ms, func(i, j int) bool { return ms[i].Name < ms[j].Name })
	// HELP 行只输出一次
	seenHelp := map[string]bool{}
	for _, m := range ms {
		if !seenHelp[m.Name] {
			fmt.Fprintf(w, "# HELP %s %s\n", m.Name, escapeHelp(m.Help))
			fmt.Fprintf(w, "# TYPE %s %s\n", m.Name, m.Type)
			seenHelp[m.Name] = true
		}
		labels := formatLabels(m.Labels)
		fmt.Fprintf(w, "%s%s %s\n", m.Name, labels, formatValue(m.Value))
	}
	return nil
}

func formatLabels(labels map[string]string) string {
	if len(labels) == 0 {
		return ""
	}
	keys := make([]string, 0, len(labels))
	for k := range labels {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	b.WriteByte('{')
	for i, k := range keys {
		if i > 0 {
			b.WriteByte(',')
		}
		fmt.Fprintf(&b, `%s="%s"`, k, escapeLabel(labels[k]))
	}
	b.WriteByte('}')
	return b.String()
}

func formatValue(v float64) string {
	// 整数用 d 格式
	if v == float64(int64(v)) {
		return fmt.Sprintf("%d", int64(v))
	}
	return fmt.Sprintf("%g", v)
}

func escapeLabel(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	s = strings.ReplaceAll(s, "\n", `\n`)
	return s
}

func escapeHelp(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, "\n", `\n`)
	return s
}
