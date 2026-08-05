import { defineStore } from 'pinia'
import { getDashboardSummary } from '@/api/dashboard'
import { getDNSStats } from '@/api/dns'
import { listPreheatTasks } from '@/api/preheat'
import { getCleanupTasks } from '@/api/cleanup'
import { listResourceRules } from '@/api/resources'
import { getAccessLogs } from '@/api/logs'
import type {
  AccessLogItem,
  CleanupTask,
  DNSStats,
  DashboardSummary,
  PreheatTask,
  ResourceRule,
} from '@/types/api'

interface State {
  data: DashboardSummary | null
  preheat: PreheatTask[]
  cleanup: CleanupTask[]
  dnsStats: DNSStats | null
  resources: ResourceRule[]
  recentLogs: AccessLogItem[]
  loading: boolean
  errorMessage: string
  lastFetchedAt: number
}

export const useDashboardStore = defineStore('dashboard', {
  state: (): State => ({
    data: null,
    preheat: [],
    cleanup: [],
    dnsStats: null,
    resources: [],
    recentLogs: [],
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
    bytesHuman: () => (n: number | null | undefined): string => {
      if (n == null) return '—'
      if (n < 1024) return `${n} B`
      if (n < 1024 * 1024) return `${(n / 1024).toFixed(1)} KB`
      if (n < 1024 * 1024 * 1024) return `${(n / 1024 / 1024).toFixed(1)} MB`
      return `${(n / 1024 / 1024 / 1024).toFixed(2)} GB`
    },
    // ---- 业务模块聚合 ----
    /** 启用的 preheat 任务数 */
    preheatActiveCount: (state) => state.preheat.filter((p) => p.enabled).length,
    /** 当前正在跑的 preheat 任务 */
    preheatRunning: (state) => state.preheat.filter((p) => p.status === 'running'),
    /** 最近一次运行的 preheat 任务（按 lastRunAt desc） */
    preheatLastRun: (state) => {
      const sorted = [...state.preheat]
        .filter((p) => p.lastRunAt > 0)
        .sort((a, b) => b.lastRunAt - a.lastRunAt)
      return sorted[0] ?? null
    },
    /** 启用的 cleanup 任务数 */
    cleanupActiveCount: (state) => state.cleanup.filter((c) => c.enabled).length,
    /** 最近一次运行的 cleanup */
    cleanupLastRun: (state) => {
      const sorted = [...state.cleanup]
        .filter((c) => c.lastRunAt > 0)
        .sort((a, b) => b.lastRunAt - a.lastRunAt)
      return sorted[0] ?? null
    },
    /** 启用的资源规则数 */
    resourcesEnabledCount: (state) => state.resources.filter((r) => r.enabled).length,
    /** DNS 24h 命中率（百分比） */
    dnsHitRate: (state) => {
      if (!state.dnsStats) return 0
      const total = state.dnsStats.hitQueries + state.dnsStats.missQueries
      return total === 0 ? 0 : (state.dnsStats.hitQueries * 100) / total
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
    /**
     * 拉所有 dashboard 关联数据（并行，单个失败不影响其他）。
     * 用于 DashboardView 一次加载。
     */
    async fetchAll(): Promise<void> {
      this.loading = true
      this.errorMessage = ''
      // 用 allSettled 让单个 API 失败不阻断其他
      const results = await Promise.allSettled([
        getDashboardSummary().then((d) => (this.data = d)),
        listPreheatTasks().then((r) => (this.preheat = r.items || [])),
        getCleanupTasks().then((c) => (this.cleanup = c)),
        getDNSStats().then((s) => (this.dnsStats = s)),
        listResourceRules().then((r) => (this.resources = r.items || [])),
        getAccessLogs(1, 8).then((r) => (this.recentLogs = r.items || [])),
      ])
      // 收集失败信息（仅显示第一个）
      const firstErr = results.find((r) => r.status === 'rejected') as
        | PromiseRejectedResult
        | undefined
      if (firstErr) {
        this.errorMessage = String(firstErr.reason?.message || firstErr.reason)
      }
      this.lastFetchedAt = Date.now()
      this.loading = false
    },
  },
})
