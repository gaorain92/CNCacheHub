// 代理访问控制 API 客户端（PRD §9.7.2 P2#4）。
import { api } from './client'

export interface AccessControlConfig {
  enabled: boolean
  /** GET 永远为 ""（masked）；PUT 用明文 */
  token: string
  tokenSet: boolean
  ipWhitelist: string[]
  loopbackBypass: boolean
  updatedAt?: number
}

export interface AccessControlPatch {
  enabled?: boolean
  /** PUT 时不传 = 不变；空字符串 = 不变（与 GET 兼容）；非空 = 替换 */
  token?: string
  ipWhitelist?: string[]
  loopbackBypass?: boolean
}

export function getAccessControl(): Promise<AccessControlConfig> {
  return api.get('/access-control').then((r) => r.data as AccessControlConfig)
}

export function updateAccessControl(patch: AccessControlPatch): Promise<AccessControlConfig> {
  return api.put('/access-control', patch).then((r) => r.data as AccessControlConfig)
}
