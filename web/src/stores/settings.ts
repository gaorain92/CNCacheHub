import { defineStore } from 'pinia'
import { ref } from 'vue'
import * as api from '@/api/settings'
import type { SystemSettings, SettingsPatch } from '@/types/api'

/**
 * 系统设置 store。
 *
 * 启动时 fetch 一次；用户改值后 updateSettings 调 PATCH，
 * 后端返回最新值覆盖。
 */
export const useSettingsStore = defineStore('settings', () => {
  const data = ref<SystemSettings | null>(null)
  const loading = ref(false)
  const errorMessage = ref('')

  async function fetch(): Promise<void> {
    loading.value = true
    errorMessage.value = ''
    try {
      data.value = await api.getSettings()
    } catch (e) {
      errorMessage.value = (e as Error).message
    } finally {
      loading.value = false
    }
  }

  async function patch(p: SettingsPatch): Promise<boolean> {
    loading.value = true
    errorMessage.value = ''
    try {
      const updated = await api.updateSettings(p)
      data.value = updated
      return true
    } catch (e) {
      errorMessage.value = (e as Error).message
      return false
    } finally {
      loading.value = false
    }
  }

  return { data, loading, errorMessage, fetch, patch }
})
