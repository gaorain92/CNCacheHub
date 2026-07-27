import { defineStore } from 'pinia'
import { getAccessLogs, purgeAccessLogs } from '@/api/logs'
import type { AccessLogItem, LogFilter } from '@/types/api'

interface State {
  items: AccessLogItem[]
  total: number
  page: number
  pageSize: number
  loading: boolean
  errorMessage: string
  // 筛选
  filter: LogFilter
}

export const useLogsStore = defineStore('logs', {
  state: (): State => ({
    items: [],
    total: 0,
    page: 1,
    pageSize: 50,
    loading: false,
    errorMessage: '',
    filter: {},
  }),
  actions: {
    async fetch(): Promise<void> {
      this.loading = true
      this.errorMessage = ''
      try {
        const resp = await getAccessLogs(this.page, this.pageSize, this.filter)
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
    setFilter(f: LogFilter): void {
      this.filter = { ...f }
      this.page = 1
      this.fetch()
    },
    clearFilter(): void {
      this.filter = {}
      this.page = 1
      this.fetch()
    },
    async purge(days = 30): Promise<{ deleted: number }> {
      const resp = await purgeAccessLogs(days)
      // 刷新列表
      await this.fetch()
      return { deleted: resp.deleted }
    },
  },
})
