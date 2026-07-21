import { defineStore } from 'pinia'
import { getUpstreamHealth } from '@/api/upstream'
import type { UpstreamHealth } from '@/types/api'

interface State {
  data: UpstreamHealth | null
  loading: boolean
  errorMessage: string
  lastFetchedAt: number
}

export const useUpstreamStore = defineStore('upstream', {
  state: (): State => ({
    data: null,
    loading: false,
    errorMessage: '',
    lastFetchedAt: 0,
  }),
  actions: {
    async fetch(): Promise<void> {
      this.loading = true
      try {
        this.data = await getUpstreamHealth()
        this.lastFetchedAt = Date.now()
      } catch (e) {
        this.errorMessage = (e as Error).message
      } finally {
        this.loading = false
      }
    },
  },
})
