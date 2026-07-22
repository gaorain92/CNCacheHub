import { api } from './client'
import type { Registry, RegistryPatch } from '@/types/api'

/**
 * 多 Registry 代理 API 客户端（PRD §9.2.2）。
 */

export async function getRegistries(): Promise<Registry[]> {
  const { data } = await api.get<{ items: Registry[]; total: number }>('/registries')
  return data.items
}

export async function setRegistryEnabled(name: string, enabled: boolean): Promise<void> {
  await api.patch(`/registries/${encodeURIComponent(name)}`, { enabled } as RegistryPatch)
}
