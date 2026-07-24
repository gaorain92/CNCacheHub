import { api } from './client'
import type {
  ResourceCacheResponse,
  ResourceRule,
  ResourceRuleCreate,
  ResourceRulePatch,
  ResourceRuleResponse,
  ResourceTemplateResponse,
} from '@/types/api'

/**
 * 资源加速中心 API 客户端（PRD §9.4 / §9.5.5 / P2#1）。
 */

export async function listResourceRules(): Promise<ResourceRuleResponse> {
  const { data } = await api.get<ResourceRuleResponse>('/resources/rules')
  return data
}

export async function createResourceRule(body: ResourceRuleCreate): Promise<ResourceRule> {
  const { data } = await api.post<ResourceRule>('/resources/rules', body)
  return data
}

export async function patchResourceRule(id: number, patch: ResourceRulePatch): Promise<ResourceRule> {
  const { data } = await api.patch<ResourceRule>(`/resources/rules/${id}`, patch)
  return data
}

export async function deleteResourceRule(id: number): Promise<void> {
  await api.delete(`/resources/rules/${id}`)
}

export async function listResourceCache(ruleID: number, limit = 100): Promise<ResourceCacheResponse> {
  const { data } = await api.get<ResourceCacheResponse>(`/resources/rules/${ruleID}/cache`, {
    params: { limit },
  })
  return data
}

export async function deleteResourceCacheEntry(id: number): Promise<void> {
  await api.delete(`/resources/cache/${id}`)
}

// listResourceTemplates 拿内置推荐模板（P2#1）
export async function listResourceTemplates(): Promise<ResourceTemplateResponse> {
  const { data } = await api.get<ResourceTemplateResponse>('/resources/templates')
  return data
}
