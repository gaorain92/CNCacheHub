// 后端响应类型定义
// 命名风格：与 Go 后端 JSON tag（camelCase）保持一致
// 字段尽量保持可选，避免在版本演进时阻塞前端

/**
 * GET /api/healthz
 * 后端至少返回 { status: "ok" }，其它字段为可选扩展
 */
export interface HealthResponse {
  status: string
  uptime?: string
  db?: string
  version?: string
}

/**
 * GET /api/version
 * 返回后端元信息
 */
export interface VersionResponse {
  name: string
  version: string
  go: string
  commit: string
}

// ====================================================================
// Phase 1 — Docker pull-through cache
// ====================================================================

/**
 * GET /api/docker/upstreams
 * 单条 upstream 配置
 */
export interface Upstream {
  id: number
  name: string
  upstreamUrl: string
  mirrorPath: string
  enabled: boolean
}

export interface UpstreamsResponse {
  items: Upstream[]
  total: number
}

/**
 * GET /api/dashboard/summary
 */
export interface DashboardSummary {
  cacheEntries: number
  cacheBytes: number
  hitCount: number
  missCount: number
  requestCount24h: number
  errorCount24h: number
  bytesOut24h: number
  activeUpstreams: number
  generatedAt: number
}

/**
 * GET /api/logs
 * 单条 access log
 */
export interface AccessLogItem {
  method: string
  path: string
  status: number
  durationMs: number
  cached: boolean
  bypassed: boolean
  clientIp: string
  bytes: number
  error: string
}

export interface AccessLogsResponse {
  items: AccessLogItem[]
  total: number
  page: number
  pageSize: number
}
