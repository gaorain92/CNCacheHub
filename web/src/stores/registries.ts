import { defineStore } from 'pinia'
import { ref } from 'vue'
import * as api from '@/api/registries'
import type { Registry, RegistryPatch } from '@/types/api'

/**
 * 多 Registry 代理 store（PRD §9.2.2 + §9.7.3）。
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
      const idx = items.value.findIndex((r) => r.name === name)
      if (idx >= 0) {
        items.value[idx] = { ...items.value[idx], enabled }
      }
      return true
    } catch (e) {
      errorMessage.value = (e as Error).message
      await fetch()
      return false
    }
  }

  // 通用 patch（含 username/password/token/clear*）— §9.7.3
  async function patch(name: string, p: RegistryPatch): Promise<Registry | null> {
    try {
      const updated = await api.patchRegistry(name, p)
      // 局部更新列表
      const idx = items.value.findIndex((r) => r.name === name)
      if (idx >= 0) {
        items.value[idx] = { ...items.value[idx], ...updated }
      }
      return updated
    } catch (e) {
      errorMessage.value = (e as Error).message
      return null
    }
  }

  return { items, loading, errorMessage, fetch, setEnabled, patch }
})

