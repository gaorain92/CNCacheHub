import { api } from './client'
import type {
  DNSConfigPatch,
  DNSConfigResponse,
  DNSStats,
  DNSTestResponse,
} from '@/types/api'

/**
 * SteamCMD DNS 启动器 API 客户端（PRD §9.3）。
 */

export async function getDNSConfig(): Promise<DNSConfigResponse> {
  const { data } = await api.get<DNSConfigResponse>('/dns/config')
  return data
}

export async function patchDNSConfig(patch: DNSConfigPatch): Promise<DNSConfigResponse> {
  const { data } = await api.patch<DNSConfigResponse>('/dns/config', patch)
  return data
}

export async function getDNSStats(): Promise<DNSStats> {
  const { data } = await api.get<DNSStats>('/dns/stats')
  return data
}

/**
 * DNS 测试：dig 域名（走 server 自身 listenAddr 或 upstream）。
 * POST body: { domain }；GET 形式用 ?domain=...。
 */
export async function testDNS(domain: string): Promise<DNSTestResponse> {
  const { data } = await api.post<DNSTestResponse>('/dns/test', { domain })
  return data
}
