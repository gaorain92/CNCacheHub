import { defineStore } from 'pinia'
import { ref } from 'vue'
import {
  cancelPreheatTask,
  createPreheatTask,
  deletePreheatTask,
  listPreheatItems,
  listPreheatTasks,
  runPreheatTask,
} from '@/api/preheat'
import type {
  PreheatItem,
  PreheatTask,
  PreheatTaskCreate,
} from '@/types/api'

export const usePreheatStore = defineStore('preheat', () => {
  const tasks = ref<PreheatTask[]>([])
  const total = ref(0)
  const loading = ref(false)
  const errorMessage = ref('')

  // 当前展开的任务 item 缓存
  const items = ref<Record<number, PreheatItem[]>>({})

  async function fetch(): Promise<boolean> {
    loading.value = true
    errorMessage.value = ''
    try {
      const resp = await listPreheatTasks()
      tasks.value = resp.items
      total.value = resp.total
      return true
    } catch (e) {
      errorMessage.value = (e as Error).message
      return false
    } finally {
      loading.value = false
    }
  }

  async function create(body: PreheatTaskCreate): Promise<PreheatTask | null> {
    errorMessage.value = ''
    try {
      const t = await createPreheatTask(body)
      tasks.value = [t, ...tasks.value]
      total.value = tasks.value.length
      return t
    } catch (e) {
      errorMessage.value = (e as Error).message
      return null
    }
  }

  async function remove(id: number): Promise<boolean> {
    errorMessage.value = ''
    try {
      await deletePreheatTask(id)
      tasks.value = tasks.value.filter((t) => t.id !== id)
      total.value = tasks.value.length
      delete items.value[id]
      return true
    } catch (e) {
      errorMessage.value = (e as Error).message
      return false
    }
  }

  async function run(id: number): Promise<PreheatTask | null> {
    errorMessage.value = ''
    try {
      const t = await runPreheatTask(id)
      const idx = tasks.value.findIndex((x) => x.id === id)
      if (idx >= 0) tasks.value[idx] = t
      return t
    } catch (e) {
      errorMessage.value = (e as Error).message
      return null
    }
  }

  async function cancel(id: number): Promise<boolean> {
    errorMessage.value = ''
    try {
      await cancelPreheatTask(id)
      // 乐观更新
      const t = tasks.value.find((x) => x.id === id)
      if (t) t.status = 'canceled'
      return true
    } catch (e) {
      errorMessage.value = (e as Error).message
      return false
    }
  }

  async function fetchItems(id: number): Promise<PreheatItem[]> {
    try {
      const resp = await listPreheatItems(id)
      items.value = { ...items.value, [id]: resp.items }
      return resp.items
    } catch (e) {
      errorMessage.value = (e as Error).message
      return []
    }
  }

  return { tasks, total, loading, errorMessage, items, fetch, create, remove, run, cancel, fetchItems }
})
