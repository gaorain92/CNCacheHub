// 代理访问控制 Pinia store（P2#4）。
import { defineStore } from 'pinia'
import { ref } from 'vue'
import {
  type AccessControlConfig,
  type AccessControlPatch,
  getAccessControl,
  updateAccessControl,
} from '@/api/access'

export const useAccessStore = defineStore('access', () => {
  const data = ref<AccessControlConfig | null>(null)
  const loading = ref(false)
  const saving = ref(false)
  const errorMessage = ref<string>('')

  async function fetch(): Promise<void> {
    loading.value = true
    errorMessage.value = ''
    try {
      data.value = await getAccessControl()
    } catch (e: any) {
      errorMessage.value = e?.response?.data?.error?.message || e?.message || '加载失败'
    } finally {
      loading.value = false
    }
  }

  async function save(patch: AccessControlPatch): Promise<boolean> {
    saving.value = true
    errorMessage.value = ''
    try {
      data.value = await updateAccessControl(patch)
      return true
    } catch (e: any) {
      errorMessage.value = e?.response?.data?.error?.message || e?.message || '保存失败'
      return false
    } finally {
      saving.value = false
    }
  }

  return { data, loading, saving, errorMessage, fetch, save }
})
