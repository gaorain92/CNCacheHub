import { api } from './client'
import type {
  PreheatItemResponse,
  PreheatTask,
  PreheatTaskCreate,
  PreheatTaskResponse,
} from '@/types/api'

/**
 * 通用预热任务 API 客户端（PRD §9.2.3 / §9.5.5）。
 */

export async function listPreheatTasks(): Promise<PreheatTaskResponse> {
  const { data } = await api.get<PreheatTaskResponse>('/preheat/tasks')
  return data
}

export async function createPreheatTask(body: PreheatTaskCreate): Promise<PreheatTask> {
  const { data } = await api.post<PreheatTask>('/preheat/tasks', body)
  return data
}

export async function deletePreheatTask(id: number): Promise<void> {
  await api.delete(`/preheat/tasks/${id}`)
}

export async function runPreheatTask(id: number): Promise<PreheatTask> {
  const { data } = await api.post<PreheatTask>(`/preheat/tasks/${id}/run`)
  return data
}

export async function cancelPreheatTask(id: number): Promise<void> {
  await api.post(`/preheat/tasks/${id}/cancel`)
}

export async function listPreheatItems(id: number): Promise<PreheatItemResponse> {
  const { data } = await api.get<PreheatItemResponse>(`/preheat/tasks/${id}/items`)
  return data
}
