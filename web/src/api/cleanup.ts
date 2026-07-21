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
 * 实际跑（删文件 + 删 DB 行）。
 */
export async function runCleanupTask(id: number): Promise<CleanupReport> {
  const { data } = await api.post<CleanupReport>(`/cleanup/tasks/${id}/run`)
  return data
}

/**
 * POST /api/cleanup/tasks/:id/dry-run
 * 干跑（不删行、不删文件），返回预估 freed_count/freed_bytes（PRD §9.6.5）。
 */
export async function dryRunCleanupTask(id: number): Promise<CleanupReport> {
  const { data } = await api.post<CleanupReport>(`/cleanup/tasks/${id}/dry-run`)
  return data
}
