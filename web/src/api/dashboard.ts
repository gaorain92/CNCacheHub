import { api } from './client'
import type { DashboardSummary } from '@/types/api'

/**
 * GET /api/dashboard/summary
 * 仪表盘聚合数据
 */
export async function getDashboardSummary(): Promise<DashboardSummary> {
  const { data } = await api.get<DashboardSummary>('/dashboard/summary')
  return data
}
