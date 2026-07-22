import { defineStore } from 'pinia'
import { ref } from 'vue'
import * as api from '@/api/client-config'
import type { ClientConfigFormat, ClientConfigResponse } from '@/api/client-config'

/**
 * 客户端配置生成器 store。
 */
export const useClientConfigStore = defineStore('client-config', () => {
  const data = ref<ClientConfigResponse | null>(null)
  const loading = ref(false)
  const errorMessage = ref('')

  async function generate(format: ClientConfigFormat, registry: string): Promise<boolean> {
    loading.value = true
    errorMessage.value = ''
    try {
      data.value = await api.generateClientConfig(format, registry)
      return true
    } catch (e) {
      errorMessage.value = (e as Error).message
      data.value = null
      return false
    } finally {
      loading.value = false
    }
  }

  function clear(): void {
    data.value = null
    errorMessage.value = ''
  }

  return { data, loading, errorMessage, generate, clear }
})
