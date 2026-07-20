import { defineStore } from 'pinia'
import { getUpstreams, getDaemonJson } from '@/api/docker'
import type { Upstream } from '@/types/api'

interface State {
  upstreams: Upstream[]
  loading: boolean
  errorMessage: string
  daemonJson: string
  daemonJsonFor: string
}

export const useDockerStore = defineStore('docker', {
  state: (): State => ({
    upstreams: [],
    loading: false,
    errorMessage: '',
    daemonJson: '',
    daemonJsonFor: '',
  }),
  actions: {
    async fetchUpstreams(): Promise<void> {
      this.loading = true
      this.errorMessage = ''
      try {
        this.upstreams = await getUpstreams()
      } catch (e) {
        this.errorMessage = (e as Error).message
      } finally {
        this.loading = false
      }
    },
    async fetchDaemonJson(): Promise<void> {
      try {
        const { content, name } = await getDaemonJson()
        this.daemonJson = content
        this.daemonJsonFor = name
      } catch (e) {
        this.errorMessage = (e as Error).message
      }
    },
  },
})
