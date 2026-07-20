import { api } from './client'
import type { CacheEntriesResponse } from '@/types/api'

/**
 * GET /api/cache/entries?page=1&pageSize=20&q=nginx
 */
export async function getCacheEntries(
  page = 1,
  pageSize = 20,
  query = ''
): Promise<CacheEntriesResponse> {
  const { data } = await api.get<CacheEntriesResponse>('/cache/entries', {
    params: { page, pageSize, q: query || undefined },
  })
  return data
}

/**
 * DELETE /api/cache/entries/:id
 */
export async function deleteCacheEntry(id: number): Promise<{ id: number; deleted: boolean }> {
  const { data } = await api.delete<{ id: number; deleted: boolean }>(`/cache/entries/${id}`)
  return data
}
