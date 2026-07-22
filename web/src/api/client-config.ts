import { api } from './client'

/**
 * 客户端配置生成器 API（PRD §9.5）。
 *
 * 公开端点 — 不需要登录（daemon 拉配置要能匿名）。
 */

export type ClientConfigFormat = 'containerd-hosts' | 'k3s-registries'

export interface ClientConfigResponse {
  registry: string
  hostname: string
  format: ClientConfigFormat
  content: string
  targetPath: string
  restartCmd: string
  verifyCmd: string
  generatedAt: number
  cnchBaseUrl: string
}

/**
 * POST /api/client-config?format=containerd-hosts&registry=ghcr
 */
export async function generateClientConfig(
  format: ClientConfigFormat,
  registry: string
): Promise<ClientConfigResponse> {
  const { data } = await api.post<ClientConfigResponse>('/client-config', null, {
    params: { format, registry },
  })
  return data
}
