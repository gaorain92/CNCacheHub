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
