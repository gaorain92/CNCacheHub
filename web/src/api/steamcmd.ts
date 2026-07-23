import { api } from './client'
import type {
  PreheatResponse,
  SteamAppID,
  SteamAppIDCreate,
  SteamAppIDPatch,
  SteamAppIDResponse,
} from '@/types/api'

/**
 * SteamCMD AppID 管理 API 客户端（PRD §9.3.3）。
 */

export async function listSteamAppIDs(): Promise<SteamAppIDResponse> {
  const { data } = await api.get<SteamAppIDResponse>('/steamcmd/appids')
  return data
}

export async function createSteamAppID(body: SteamAppIDCreate): Promise<SteamAppID> {
  const { data } = await api.post<SteamAppID>('/steamcmd/appids', body)
  return data
}

export async function patchSteamAppID(id: number, patch: SteamAppIDPatch): Promise<SteamAppID> {
  const { data } = await api.patch<SteamAppID>(`/steamcmd/appids/${id}`, patch)
  return data
}

export async function deleteSteamAppID(id: number): Promise<void> {
  await api.delete(`/steamcmd/appids/${id}`)
}

export async function preheatSteamAppID(id: number): Promise<PreheatResponse> {
  const { data } = await api.post<PreheatResponse>(`/steamcmd/appids/${id}/preheat`)
  return data
}
