import { defineStore } from 'pinia'
import { ref } from 'vue'
import {
  getDNSConfig,
  getDNSStats,
  patchDNSConfig,
  testDNS,
} from '@/api/dns'
import type {
  DNSConfig,
  DNSConfigPatch,
  DNSConfigResponse,
  DNSStats,
  DNSTestResponse,
} from '@/types/api'

export const useDNSStore = defineStore('dns', () => {
  const config = ref<DNSConfig | null>(null)
  const stats = ref<DNSStats | null>(null)
  const loading = ref(false)
  const saving = ref(false)
  const testing = ref(false)
  const errorMessage = ref('')
  const lastTest = ref<DNSTestResponse | null>(null)

  async function fetch(): Promise<boolean> {
    loading.value = true
    errorMessage.value = ''
    try {
      const resp: DNSConfigResponse = await getDNSConfig()
      config.value = resp.config
      stats.value = resp.stats
      return true
    } catch (e) {
      errorMessage.value = (e as Error).message
      return false
    } finally {
      loading.value = false
    }
  }

  async function fetchStats(): Promise<boolean> {
    try {
      stats.value = await getDNSStats()
      return true
    } catch {
      return false
    }
  }

  async function patch(p: DNSConfigPatch): Promise<boolean> {
    saving.value = true
    errorMessage.value = ''
    try {
      const resp = await patchDNSConfig(p)
      config.value = resp.config
      stats.value = resp.stats
      return true
    } catch (e) {
      errorMessage.value = (e as Error).message
      return false
    } finally {
      saving.value = false
    }
  }

  async function test(domain: string): Promise<DNSTestResponse | null> {
    testing.value = true
    try {
      const resp = await testDNS(domain)
      lastTest.value = resp
      // 顺手刷一下 stats
      await fetchStats()
      return resp
    } finally {
      testing.value = false
    }
  }

  return { config, stats, loading, saving, testing, errorMessage, lastTest, fetch, fetchStats, patch, test }
})
