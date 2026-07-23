import { defineStore } from 'pinia'
import { ref } from 'vue'
import {
  createSteamAppID,
  deleteSteamAppID,
  listSteamAppIDs,
  patchSteamAppID,
  preheatSteamAppID,
} from '@/api/steamcmd'
import type {
  PreheatResponse,
  SteamAppID,
  SteamAppIDCreate,
  SteamAppIDPatch,
} from '@/types/api'

export const useSteamcmdStore = defineStore('steamcmd', () => {
  const items = ref<SteamAppID[]>([])
  const total = ref(0)
  const loading = ref(false)
  const errorMessage = ref('')

  async function fetch(): Promise<boolean> {
    loading.value = true
    errorMessage.value = ''
    try {
      const resp = await listSteamAppIDs()
      items.value = resp.items
      total.value = resp.total
      return true
    } catch (e) {
      errorMessage.value = (e as Error).message
      return false
    } finally {
      loading.value = false
    }
  }

  async function create(body: SteamAppIDCreate): Promise<SteamAppID | null> {
    errorMessage.value = ''
    try {
      const a = await createSteamAppID(body)
      items.value = [...items.value, a].sort((x, y) => x.appId - y.appId)
      total.value = items.value.length
      return a
    } catch (e) {
      errorMessage.value = (e as Error).message
      return null
    }
  }

  async function patch(id: number, p: SteamAppIDPatch): Promise<SteamAppID | null> {
    errorMessage.value = ''
    try {
      const a = await patchSteamAppID(id, p)
      const idx = items.value.findIndex((x) => x.id === id)
      if (idx >= 0) items.value[idx] = a
      return a
    } catch (e) {
      errorMessage.value = (e as Error).message
      return null
    }
  }

  async function remove(id: number): Promise<boolean> {
    errorMessage.value = ''
    try {
      await deleteSteamAppID(id)
      items.value = items.value.filter((x) => x.id !== id)
      total.value = items.value.length
      return true
    } catch (e) {
      errorMessage.value = (e as Error).message
      return false
    }
  }

  async function preheat(id: number): Promise<PreheatResponse | null> {
    errorMessage.value = ''
    try {
      return await preheatSteamAppID(id)
    } catch (e) {
      errorMessage.value = (e as Error).message
      return null
    }
  }

  return { items, total, loading, errorMessage, fetch, create, patch, remove, preheat }
})
