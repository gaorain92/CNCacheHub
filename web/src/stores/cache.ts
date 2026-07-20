import { defineStore } from 'pinia'
import { getCacheEntries, deleteCacheEntry } from '@/api/cache'
import type { CacheEntry } from '@/types/api'

interface State {
  items: CacheEntry[]
  total: number
  page: number
  pageSize: number
  query: string
  loading: boolean
  errorMessage: string
}

export const useCacheStore = defineStore('cache', {
  state: (): State => ({
    items: [],
    total: 0,
    page: 1,
    pageSize: 20,
    query: '',
    loading: false,
    errorMessage: '',
  }),
  getters: {
    totalBytes: (state) => state.items.reduce((s, e) => s + e.sizeBytes, 0),
    totalHits: (state) => state.items.reduce((s, e) => s + e.hitCount, 0),
  },
  actions: {
    async fetch(): Promise<void> {
      this.loading = true
      this.errorMessage = ''
      try {
        const resp = await getCacheEntries(this.page, this.pageSize, this.query)
        this.items = resp.items
        this.total = resp.total
        this.page = resp.page
        this.pageSize = resp.pageSize
      } catch (e) {
        this.errorMessage = (e as Error).message
      } finally {
        this.loading = false
      }
    },
    async remove(id: number): Promise<boolean> {
      try {
        await deleteCacheEntry(id)
        // 重新拉一次（list 状态会变）
        await this.fetch()
        return true
      } catch (e) {
        this.errorMessage = (e as Error).message
        return false
      }
    },
    setPage(p: number): void {
      this.page = p
      this.fetch()
    },
    setQuery(q: string): void {
      this.query = q
      this.page = 1
      this.fetch()
    },
  },
})
