import { api } from './client'
import type { AccessLogsResponse } from '@/types/api'

/**
 * GET /api/logs?page=1&pageSize=50
 */
export async function getAccessLogs(page = 1, pageSize = 50): Promise<AccessLogsResponse> {
  const { data } = await api.get<AccessLogsResponse>('/logs', {
    params: { page, pageSize },
  })
  return data
}
