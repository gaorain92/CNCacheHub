import { defineStore } from 'pinia'
import { ref } from 'vue'
import * as api from '@/api/registries'
import type { Registry } from '@/types/api'

/**
 * 多 Registry 代理 store（PRD §9.2.2）。
 */
export const useRegistriesStore = defineStore('registries', () => {
  const items = ref<Registry[]>([])
  const loading = ref(false)
  const errorMessage = ref('')

  async function fetch(): Promise<void> {
    loading.value = true
    errorMessage.value = ''
    try {
      items.value = await api.getRegistries()
    } catch (e) {
      errorMessage.value = (e as Error).message
    } finally {
      loading.value = false
    }
  }

  async function setEnabled(name: string, enabled: boolean): Promise<boolean> {
    try {
      await api.setRegistryEnabled(name, enabled)
      // 本地乐观更新
      const idx = items.value.findIndex((r) => r.name === name)
      if (idx >= 0) {
        items.value[idx] = { ...items.value[idx], enabled }
      }
      return true
    } catch (e) {
      errorMessage.value = (e as Error).message
      // 失败：刷新拉一次
      await fetch()
      return false
    }
  }

  return { items, loading, errorMessage, fetch, setEnabled }
})
