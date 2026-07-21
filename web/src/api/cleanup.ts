import { api } from './client'
import type { CleanupTask, CleanupReport } from '@/types/api'

/**
 * GET /api/cleanup/tasks
 */
export async function getCleanupTasks(): Promise<CleanupTask[]> {
  const { data } = await api.get<{ items: CleanupTask[]; total: number }>('/cleanup/tasks')
  return data.items
}

/**
 * POST /api/cleanup/tasks/:id/run
 */
export async function runCleanupTask(id: number): Promise<CleanupReport> {
  const { data } = await api.post<CleanupReport>(`/cleanup/tasks/${id}/run`)
  return data
}
