import { api } from './client'
import type { SystemSettings, SettingsPatch } from '@/types/api'

/**
 * 系统设置 API（PRD §9.1.4 + §9.6.4）。
 *
 * 注意：GET 不需要 admin；PATCH 必须 admin。
 */

export async function getSettings(): Promise<SystemSettings> {
  const { data } = await api.get<SystemSettings>('/settings')
  return data
}

export async function updateSettings(patch: SettingsPatch): Promise<SystemSettings> {
  const { data } = await api.patch<SystemSettings>('/settings', patch)
  return data
}
