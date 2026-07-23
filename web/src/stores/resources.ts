import { defineStore } from 'pinia'
import { ref } from 'vue'
import {
  createResourceRule,
  deleteResourceCacheEntry,
  deleteResourceRule,
  listResourceCache,
  listResourceRules,
  patchResourceRule,
} from '@/api/resources'
import type {
  ResourceRule,
  ResourceRuleCreate,
  ResourceRulePatch,
} from '@/types/api'

export const useResourcesStore = defineStore('resources', () => {
  const rules = ref<ResourceRule[]>([])
  const total = ref(0)
  const loading = ref(false)
  const errorMessage = ref('')

  // 每个 rule 的 cache 列表
  const caches = ref<Record<number, any[]>>({})

  async function fetch(): Promise<boolean> {
    loading.value = true
    errorMessage.value = ''
    try {
      const resp = await listResourceRules()
      rules.value = resp.items
      total.value = resp.total
      return true
    } catch (e) {
      errorMessage.value = (e as Error).message
      return false
    } finally {
      loading.value = false
    }
  }

  async function create(body: ResourceRuleCreate): Promise<ResourceRule | null> {
    errorMessage.value = ''
    try {
      const r = await createResourceRule(body)
      rules.value = [...rules.value, r]
      total.value = rules.value.length
      return r
    } catch (e) {
      errorMessage.value = (e as Error).message
      return null
    }
  }

  async function patch(id: number, p: ResourceRulePatch): Promise<ResourceRule | null> {
    errorMessage.value = ''
    try {
      const r = await patchResourceRule(id, p)
      const idx = rules.value.findIndex((x) => x.id === id)
      if (idx >= 0) rules.value[idx] = r
      return r
    } catch (e) {
      errorMessage.value = (e as Error).message
      return null
    }
  }

  async function remove(id: number): Promise<boolean> {
    errorMessage.value = ''
    try {
      await deleteResourceRule(id)
      rules.value = rules.value.filter((x) => x.id !== id)
      total.value = rules.value.length
      delete caches.value[id]
      return true
    } catch (e) {
      errorMessage.value = (e as Error).message
      return false
    }
  }

  async function fetchCache(ruleID: number, limit = 100): Promise<void> {
    try {
      const resp = await listResourceCache(ruleID, limit)
      caches.value = { ...caches.value, [ruleID]: resp.items }
    } catch (e) {
      errorMessage.value = (e as Error).message
    }
  }

  async function removeCache(id: number, ruleID: number): Promise<boolean> {
    errorMessage.value = ''
    try {
      await deleteResourceCacheEntry(id)
      if (caches.value[ruleID]) {
        caches.value = {
          ...caches.value,
          [ruleID]: caches.value[ruleID].filter((x) => x.id !== id),
        }
      }
      return true
    } catch (e) {
      errorMessage.value = (e as Error).message
      return false
    }
  }

  return { rules, total, loading, errorMessage, caches, fetch, create, patch, remove, fetchCache, removeCache }
})
