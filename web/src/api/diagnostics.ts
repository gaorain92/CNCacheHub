import { api } from './client'
import type { DiagFullReport } from '@/types/api'

/**
 * 诊断中心 API 客户端（PRD §9.7）。
 */

export async function runDiagnostics(): Promise<DiagFullReport> {
  const { data } = await api.get<DiagFullReport>('/diagnostics/run')
  return data
}
