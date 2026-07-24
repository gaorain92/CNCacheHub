import { api } from './client'
import type { Registry, RegistryPatch } from '@/types/api'

/**
 * 多 Registry 代理 API 客户端（PRD §9.2.2 + §9.7.3）。
 */

export async function getRegistries(): Promise<Registry[]> {
  const { data } = await api.get<{ items: Registry[]; total: number }>('/registries')
  return data.items
}

export async function patchRegistry(name: string, patch: RegistryPatch): Promise<Registry> {
  const { data } = await api.patch<Registry>(`/registries/${encodeURIComponent(name)}`, patch)
  return data
}

// 保留旧名 — 单字段 enabled 时用
export async function setRegistryEnabled(name: string, enabled: boolean): Promise<void> {
  await patchRegistry(name, { enabled })
}
