import { api } from './client'
import type { UpstreamsResponse, Upstream } from '@/types/api'

/**
 * GET /api/docker/upstreams
 * 列出所有 enabled upstream
 */
export async function getUpstreams(): Promise<Upstream[]> {
  const { data } = await api.get<UpstreamsResponse>('/docker/upstreams')
  return data.items
}

/**
 * GET /api/docker/daemon.json
 * 后端返回 raw JSON 字符串（已 prettify），直接复制即可用
 */
export async function getDaemonJson(): Promise<{ name: string; content: string }> {
  const resp = await api.get<string>('/docker/daemon.json', {
    transformResponse: [(d) => d],
    responseType: 'text',
  })
  // resp.data 是 raw JSON 字符串
  const name = resp.headers['x-cncachehub-config-for'] as string | undefined
  return { name: name || 'dockerhub', content: resp.data }
}
