// Package diagnostics 诊断包导出（PRD §9.8.3 P2#3）。
//
// 端点：POST /api/diagnostics/bundle → 流式返回 .tar.gz
//
// 内容：
//   - README.txt            — 文件清单 + 排查建议
//   - system.json           — 平台 + 启动时间 + 版本
//   - config.json           — 脱敏后的启动配置（密码/secret 字段 mask）
//   - summary.json          — dashboard 聚合（cache/24h 错误/请求数）
//   - settings.json         — system_settings 全部
//   - rules.json            — resource_rules 全部
//   - rule_cache.csv        — resource_cache_entries（每条 1 行）
//   - preheat_tasks.json    — preheat_tasks 全部
//   - preheat_items.csv     — preheat_items 全部
//   - cleanup_tasks.json    — cleanup_tasks 全部
//   - access_logs.csv       — request_logs 最近 1000 条
//   - dns_config.json       — DNS 启动器配置
//   - cache_policy.json     — 缓存策略（max_object_size/reserve/total）
//   - settings_extra.json   — 上游/端口
//   - system.log            — 可选，最后 512KB server 日志
package diagnostics

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/cncachehub/server/internal/storage"
)

// BundleSource 注入诊断包所需数据。
//
// main.go 构造 + 注入；bundle 写入只依赖 src.DB 拿数据，其它字段用来写 snapshot。
type BundleSource struct {
	DB           *storage.DB
	Version      string
	Commit       string
	StartTime    time.Time
	HTTPAddr     string
	CacheDir     string
	DataDir      string
	UpstreamURL  string
	MaxObjectMB  int
	ReserveGB    int
	CacheTotalGB int
	LogPath      string // /var/log/cncachehub/cncachehub.log 可选
}

// WriteBundle 把诊断包写到 w（gzip tar）。
//
// 错误一律 best-effort：不写哪些文件不影响整个 bundle（单条 _ = add(...) 吞错）；
// 让用户拿到 9/10 文件总比 0/10 文件强。
func WriteBundle(ctx context.Context, w io.Writer, src BundleSource) error {
	gw := gzip.NewWriter(w)
	defer gw.Close()
	tw := tar.NewWriter(gw)
	defer tw.Close()

	add := func(name string, body []byte, mode int64) error {
		h := &tar.Header{
			Name:    name,
			Mode:    mode,
			Size:    int64(len(body)),
			ModTime: time.Now(),
		}
		if err := tw.WriteHeader(h); err != nil {
			return err
		}
		_, err := tw.Write(body)
		return err
	}

	// README
	readme := `CNCacheHub 诊断包
==================

生成时间: ` + time.Now().Format(time.RFC3339) + `

文件清单:
  system.json         平台 + 启动时间 + 版本
  config.json         脱敏后的启动配置
  summary.json        dashboard 聚合数据
  settings.json       system_settings
  rules.json          resource_rules
  rule_cache.csv      resource_cache_entries
  preheat_tasks.json  preheat_tasks
  preheat_items.csv   preheat_items
  cleanup_tasks.json  cleanup_tasks
  access_logs.csv     request_logs（最近 1000 条）
  dns_config.json     DNS 启动器配置
  cache_policy.json   缓存策略
  settings_extra.json 上游/端口
  system.log          （可选）最近 server 日志

排查建议:
  1. 看 summary.json 了解 24h 错误率
  2. 看 access_logs.csv 找 4xx/5xx 模式
  3. 看 settings.json 确认 system_settings 正确
  4. 看 config.json 确认启动参数
  5. 看 dns_config.json 确认 DNS 配置（如果用 SteamCMD）
`
	_ = add("README.txt", []byte(readme), 0644)

	// system.json
	sysJSON, _ := json.MarshalIndent(map[string]any{
		"goVersion":   runtime.Version(),
		"version":     src.Version,
		"commit":      src.Commit,
		"startTime":   src.StartTime.Format(time.RFC3339),
		"uptime":      time.Since(src.StartTime).String(),
		"generatedAt": time.Now().Format(time.RFC3339),
		"hostname":    hostnameSafe(),
		"pid":         os.Getpid(),
	}, "", "  ")
	_ = add("system.json", sysJSON, 0644)

	// config.json — 启动配置（注意：admin_password 已经被 log redactor 脱敏，
	// bundle 里我们不再重复它；只在 system.json 体现 version/commit）。
	cfgJSON, _ := json.MarshalIndent(map[string]any{
		"http_addr":            src.HTTPAddr,
		"cache_dir":            src.CacheDir,
		"data_dir":             src.DataDir,
		"upstream_registry":    src.UpstreamURL,
		"max_object_size_mb":   src.MaxObjectMB,
		"reserve_space_gb":     src.ReserveGB,
		"cache_total_gb":       src.CacheTotalGB,
		"log_path":             src.LogPath,
		"shutdown_timeout_sec": 30,
	}, "", "  ")
	_ = add("config.json", cfgJSON, 0644)

	// cache_policy.json（不依赖 DB）
	policyJSON, _ := json.MarshalIndent(map[string]any{
		"max_object_size_bytes": src.MaxObjectMB * 1024 * 1024,
		"reserve_space_bytes":   src.ReserveGB * 1024 * 1024 * 1024,
		"cache_total_gb":        src.CacheTotalGB,
	}, "", "  ")
	_ = add("cache_policy.json", policyJSON, 0644)

	// settings_extra.json（不依赖 DB）
	extraJSON, _ := json.MarshalIndent(map[string]any{
		"upstream_url": src.UpstreamURL,
		"http_addr":    src.HTTPAddr,
		"data_dir":     src.DataDir,
		"cache_dir":    src.CacheDir,
	}, "", "  ")
	_ = add("settings_extra.json", extraJSON, 0644)

	// system.log（如果存在，复制文件最后 512KB 防止 bundle 太大）
	if src.LogPath != "" {
		if data, err := os.ReadFile(src.LogPath); err == nil {
			if len(data) > 512*1024 {
				data = data[len(data)-512*1024:]
			}
			_ = add("system.log", data, 0644)
		}
	}

	if src.DB == nil {
		return nil
	}

	// summary.json
	if ds, err := src.DB.DashboardSummary(ctx); err == nil {
		b, _ := json.MarshalIndent(ds, "", "  ")
		_ = add("summary.json", b, 0644)
	}

	// settings.json
	if settings, err := src.DB.ListSettings(ctx); err == nil {
		b, _ := json.MarshalIndent(map[string]any{"settings": settings}, "", "  ")
		_ = add("settings.json", b, 0644)
	}

	// rules.json
	if rules, err := src.DB.ListResourceRules(ctx); err == nil {
		b, _ := json.MarshalIndent(map[string]any{"rules": rules}, "", "  ")
		_ = add("rules.json", b, 0644)
	}

	// rule_cache.csv — 聚合（每 rule 最多 100 条）
	if rules, err := src.DB.ListResourceRules(ctx); err == nil {
		buf := []byte("rule_id,rule_name,path,size_bytes,hit_count,expires_at\n")
		for _, r := range rules {
			entries, _ := src.DB.ListResourceCache(ctx, r.ID, 100)
			for _, e := range entries {
				buf = append(buf,
					strconv.FormatInt(e.RuleID, 10)+","+
						csvSafe(r.Name)+","+
						csvSafe(e.Path)+","+
						strconv.FormatInt(e.SizeBytes, 10)+","+
						strconv.FormatInt(e.HitCount, 10)+","+
						strconv.FormatInt(e.ExpiresAt, 10)+"\n"...)
			}
		}
		_ = add("rule_cache.csv", buf, 0644)
	}

	// preheat_tasks.json
	if tasks, err := src.DB.ListPreheatTasks(ctx); err == nil {
		b, _ := json.MarshalIndent(map[string]any{"tasks": tasks}, "", "  ")
		_ = add("preheat_tasks.json", b, 0644)
	}

	// preheat_items.csv
	if tasks, err := src.DB.ListPreheatTasks(ctx); err == nil {
		buf := []byte("task_id,target,status,bytes_added,started_at,finished_at,error\n")
		for _, t := range tasks {
			items, _ := src.DB.ListPreheatItems(ctx, t.ID)
			for _, it := range items {
				buf = append(buf,
					strconv.FormatInt(it.TaskID, 10)+","+
						csvSafe(it.Target)+","+
						it.Status+","+
						strconv.FormatInt(it.BytesAdded, 10)+","+
						strconv.FormatInt(it.StartedAt, 10)+","+
						strconv.FormatInt(it.FinishedAt, 10)+","+
						csvSafe(it.ErrorMessage)+"\n"...)
			}
		}
		_ = add("preheat_items.csv", buf, 0644)
	}

	// cleanup_tasks.json
	if tasks, err := src.DB.ListCleanupTasks(ctx); err == nil {
		b, _ := json.MarshalIndent(map[string]any{"tasks": tasks}, "", "  ")
		_ = add("cleanup_tasks.json", b, 0644)
	}

	// access_logs.csv（最近 1000 条）
	if logs, _, err := src.DB.ListAccessLogs(ctx, 1, 1000); err == nil {
		buf := []byte("id,created_at,method,path,status,duration_ms,cached,bypassed,bypass_reason,client_ip,bytes,error\n")
		for _, l := range logs {
			buf = append(buf,
				strconv.FormatInt(l.ID, 10)+","+
					strconv.FormatInt(l.CreatedAt, 10)+","+
					l.Method+","+
					csvSafe(l.Path)+","+
					strconv.Itoa(l.Status)+","+
					strconv.FormatInt(l.DurationMs, 10)+","+
					strconv.FormatBool(l.Cached)+","+
					strconv.FormatBool(l.Bypassed)+","+
					l.BypassReason+","+
					l.ClientIP+","+
					strconv.FormatInt(l.Bytes, 10)+","+
					csvSafe(l.Error)+"\n"...)
		}
		_ = add("access_logs.csv", buf, 0644)
	}

	// dns_config.json
	if dnsCfg, err := src.DB.GetDNSConfig(ctx); err == nil {
		b, _ := json.MarshalIndent(dnsCfg, "", "  ")
		_ = add("dns_config.json", b, 0644)
	}

	return nil
}

// BundleHandler 返回 POST /api/diagnostics/bundle 的 http.Handler。
func BundleHandler(src BundleSource) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		filename := fmt.Sprintf("cncachehub-diagnostics-%s.tar.gz", time.Now().Format("20060102-150405"))
		w.Header().Set("Content-Type", "application/gzip")
		w.Header().Set("Content-Disposition", `attachment; filename="`+filename+`"`)
		w.WriteHeader(http.StatusOK)
		_ = WriteBundle(r.Context(), w, src)
	}
}

// === helpers ===

// csvSafe 把字符串转成 CSV 字段（含 , " \n 时整体加引号 + 转义 "）。
func csvSafe(s string) string {
	if !strings.ContainsAny(s, ",\"\n") {
		return s
	}
	return `"` + strings.ReplaceAll(s, `"`, `""`) + `"`
}

// hostnameSafe 拿主机名（拿不到返回 "unknown"）。
func hostnameSafe() string {
	h, _ := os.Hostname()
	if h == "" {
		return "unknown"
	}
	return h
}
