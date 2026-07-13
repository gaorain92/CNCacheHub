import { defineStore } from 'pinia'
import { getHealth, getVersion } from '@/api/health'
import type { HealthResponse, VersionResponse } from '@/types/api'

export type HealthStatus = 'ok' | 'down' | 'unknown'

interface HealthState {
  status: HealthStatus
  uptime: string
  db: string
  version: string
  goVersion: string
  commit: string
  backendConnected: boolean
  lastCheckedAt: number
  loading: boolean
  errorMessage: string
}

export const useHealthStore = defineStore('health', {
  state: (): HealthState => ({
    status: 'unknown',
    uptime: '—',
    db: '—',
    version: '—',
    goVersion: '—',
    commit: '—',
    backendConnected: false,
    lastCheckedAt: 0,
    loading: false,
    errorMessage: '',
  }),
  getters: {
    isHealthy: (state) => state.status === 'ok' && state.backendConnected,
  },
  actions: {
    /**
     * 拉取健康状态 + 版本信息
     * 任意一步失败均会把 backendConnected 置为 false，但不会抛错（页面需要友好降级）
     */
    async fetch(): Promise<void> {
      this.loading = true
      this.errorMessage = ''
      let health: HealthResponse | null = null
      let version: VersionResponse | null = null
      let firstError = ''

      try {
        health = await getHealth()
      } catch (e) {
        firstError = firstError || (e as Error).message
      }

      try {
        version = await getVersion()
      } catch (e) {
        firstError = firstError || (e as Error).message
      }

      if (health) {
        this.status = (health.status === 'ok' ? 'ok' : 'down') as HealthStatus
        this.uptime = health.uptime ?? this.uptime
        this.db = health.db ?? this.db
        if (health.version) this.version = health.version
      } else {
        this.status = 'down'
      }

      if (version) {
        this.version = version.version || this.version
        this.goVersion = version.go || this.goVersion
        this.commit = version.commit || this.commit
      }

      this.backendConnected = !!(health || version)
      this.lastCheckedAt = Date.now()
      this.loading = false
      if (!this.backendConnected) this.errorMessage = firstError || '后端未连接'
    },
  },
})
