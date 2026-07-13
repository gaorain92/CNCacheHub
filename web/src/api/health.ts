import { api } from './client'
import type { HealthResponse, VersionResponse } from '@/types/api'

/**
 * GET /api/healthz
 * 探测后端基础健康状态
 */
export async function getHealth(): Promise<HealthResponse> {
  const { data } = await api.get<HealthResponse>('/healthz')
  return data
}

/**
 * GET /api/version
 * 拉取后端版本元信息
 */
export async function getVersion(): Promise<VersionResponse> {
  const { data } = await api.get<VersionResponse>('/version')
  return data
}
