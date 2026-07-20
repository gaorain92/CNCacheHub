import { defineStore } from 'pinia'
import { getDashboardSummary } from '@/api/dashboard'
import type { DashboardSummary } from '@/types/api'

interface State {
  data: DashboardSummary | null
  loading: boolean
  errorMessage: string
  lastFetchedAt: number
}

export const useDashboardStore = defineStore('dashboard', {
  state: (): State => ({
    data: null,
    loading: false,
    errorMessage: '',
    lastFetchedAt: 0,
  }),
  getters: {
    hitRate: (state) => {
      if (!state.data) return 0
      const total = state.data.hitCount + state.data.missCount
      return total === 0 ? 0 : (state.data.hitCount * 100) / total
    },
    cacheBytesHuman: (state) => {
      if (!state.data) return '—'
      const b = state.data.cacheBytes
      if (b < 1024) return `${b} B`
      if (b < 1024 * 1024) return `${(b / 1024).toFixed(1)} KB`
      if (b < 1024 * 1024 * 1024) return `${(b / 1024 / 1024).toFixed(1)} MB`
      return `${(b / 1024 / 1024 / 1024).toFixed(2)} GB`
    },
    bytesOut24hHuman: (state) => {
      if (!state.data) return '—'
      const b = state.data.bytesOut24h
      if (b < 1024) return `${b} B`
      if (b < 1024 * 1024) return `${(b / 1024).toFixed(1)} KB`
      if (b < 1024 * 1024 * 1024) return `${(b / 1024 / 1024).toFixed(1)} MB`
      return `${(b / 1024 / 1024 / 1024).toFixed(2)} GB`
    },
  },
  actions: {
    async fetch(): Promise<void> {
      this.loading = true
      this.errorMessage = ''
      try {
        this.data = await getDashboardSummary()
        this.lastFetchedAt = Date.now()
      } catch (e) {
        this.errorMessage = (e as Error).message
      } finally {
        this.loading = false
      }
    },
  },
})
