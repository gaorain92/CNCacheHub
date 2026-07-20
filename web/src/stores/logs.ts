import { defineStore } from 'pinia'
import { getAccessLogs } from '@/api/logs'
import type { AccessLogItem } from '@/types/api'

interface State {
  items: AccessLogItem[]
  total: number
  page: number
  pageSize: number
  loading: boolean
  errorMessage: string
}

export const useLogsStore = defineStore('logs', {
  state: (): State => ({
    items: [],
    total: 0,
    page: 1,
    pageSize: 50,
    loading: false,
    errorMessage: '',
  }),
  actions: {
    async fetch(): Promise<void> {
      this.loading = true
      this.errorMessage = ''
      try {
        const resp = await getAccessLogs(this.page, this.pageSize)
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
    setPage(p: number): void {
      this.page = p
      this.fetch()
    },
  },
})
