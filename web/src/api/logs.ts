import { api } from './client'
import type { AccessLogsResponse, LogFilter } from '@/types/api'

/**
 * GET /api/logs?page=1&pageSize=50&...filters
 */
export async function getAccessLogs(
  page = 1,
  pageSize = 50,
  filter?: LogFilter,
): Promise<AccessLogsResponse> {
  const params: Record<string, string | number> = { page, pageSize }
  if (filter) {
    if (filter.status) params.status = filter.status
    if (filter.statusCls) params.statusCls = filter.statusCls
    if (filter.method) params.method = filter.method
    if (filter.path) params.path = filter.path
    if (filter.cached !== undefined) params.cached = String(filter.cached)
    if (filter.bypassed !== undefined) params.bypassed = String(filter.bypassed)
    if (filter.clientIp) params.clientIp = filter.clientIp
    if (filter.startAt) params.startAt = filter.startAt
    if (filter.endAt) params.endAt = filter.endAt
  }
  const { data } = await api.get<AccessLogsResponse>('/logs', { params })
  return data
}

/**
 * DELETE /api/logs?days=30  — 清除 N 天前的日志
 */
export async function purgeAccessLogs(days = 30): Promise<{ deleted: number; before: number }> {
  const { data } = await api.delete<{ deleted: number; before: number }>('/logs', {
    params: { days },
  })
  return data
}

/**
 * GET /api/logs/stats  — 日志总行数
 */
export async function getLogStats(): Promise<{ total: number }> {
  const { data } = await api.get<{ total: number }>('/logs/stats')
  return data
}
