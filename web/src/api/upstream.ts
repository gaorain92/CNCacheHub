import { api } from './client'
import type { UpstreamHealth } from '@/types/api'

/**
 * GET /api/health/upstream
 */
export async function getUpstreamHealth(): Promise<UpstreamHealth> {
  const { data } = await api.get<UpstreamHealth>('/health/upstream')
  return data
}
