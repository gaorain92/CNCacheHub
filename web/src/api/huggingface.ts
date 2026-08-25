import { api } from './client'
import type {
  HuggingFacePreheatRequest,
  HuggingFacePreheatResponse,
  HuggingFaceTreeFile,
  HuggingFaceTreeResponse,
} from '@/types/api'

/**
 * HuggingFace 模型加速（独立菜单）相关 API。
 *
 * - GET  /api/huggingface/models/{modelId}/tree?revision=main
 *   → 拉 HF 模型文件树（type=file，含 path/size）
 * - POST /api/huggingface/preheat
 *   → 创 preheat task (kind=huggingface_model) 拉全部文件
 */

export async function listHuggingFaceTree(
  modelId: string,
  revision = 'main'
): Promise<HuggingFaceTreeResponse> {
  // modelId 含 "/"，需要 URL-encode
  const encoded = encodeURIComponent(modelId)
  const { data } = await api.get<HuggingFaceTreeResponse>(
    `/huggingface/models/${encoded}/tree?revision=${encodeURIComponent(revision)}`
  )
  return data
}

export async function createHuggingFacePreheat(
  body: HuggingFacePreheatRequest
): Promise<HuggingFacePreheatResponse> {
  const { data } = await api.post<HuggingFacePreheatResponse>('/huggingface/preheat', body)
  return data
}

// 重新导出 tree file 类型方便 store 使用
export type { HuggingFaceTreeFile }
