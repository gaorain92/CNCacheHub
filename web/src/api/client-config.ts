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

/**
 * POST /api/client-config/bundle — §9.5.4 一键 zip 配置包
 *
 * 返回 Blob；调用方负责触发下载。
 */
export async function downloadClientConfigBundle(): Promise<Blob> {
  const response = await api.post<Blob>('/client-config/bundle', null, {
    responseType: 'blob',
    // axios 默认会尝试把 blob 转 JSON；显式声明 raw
    transformRequest: [(data) => data],
  })
  return response.data
}
