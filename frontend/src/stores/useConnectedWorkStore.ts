import { create } from 'zustand'
import { desktopRequest, type DesktopConnection, type DesktopInventory } from '../services/desktopConnections'
import { agentApi, getAuthToken, getDesktopApiBaseUrl, getLocalApiBaseUrl } from '../services/api'
import type { ActiveSessionInfo, HeaderSummaryResponse } from '../services/api-types'
import { captureChatIdentity, isChatIdentityCurrent } from '../utils/chatIdentity'
import { getRawActiveWorkspaceId, useWorkspaceConnectionStore } from './useWorkspaceConnectionStore'

export const localConnection: DesktopConnection = { id: 'local', name: 'Local', server: '' }
export const hasDesktopConnections = () => Boolean(window.electronAPI?.getDesktopConnection?.())
export type LocationActivity = { sessions: ActiveSessionInfo[]; status: DesktopInventory['status']; updatedAt?: number }
let directoryPending: Promise<void> | undefined
let activityPending: Promise<void> | undefined
let ownerIdentity = -1

/** Connection-tagged navigation/activity only. Remote conversations never enter the chat store. */
export const useConnectedWorkStore = create<{
  identity: number
  connections: DesktopConnection[]
  inventory: Record<string, DesktopInventory>
  activity: Record<string, LocationActivity>
  loaded: boolean
  error: string
  refresh: () => Promise<void>
  refreshActivity: () => Promise<void>
}>(set => ({
  identity: captureChatIdentity(), connections: [localConnection], inventory: {}, activity: {}, loaded: false, error: '',
  refresh: async () => {
    if (!hasDesktopConnections()) return
    const identity = captureChatIdentity()
    if (ownerIdentity !== identity) {
      ownerIdentity = identity
      directoryPending = undefined; activityPending = undefined
      set({ identity, connections: [localConnection], inventory: {}, activity: {}, loaded: false, error: '' })
    }
    if (directoryPending) return directoryPending
    const pending = (async () => {
      try {
        const data = await desktopRequest<{ connections: DesktopConnection[] }>()
        if (!isChatIdentityCurrent(identity)) return
        const connections = [localConnection, ...data.connections]
        const ids = new Set(connections.map(connection => connection.id))
        set(state => ({ connections, loaded: true, error: '',
          inventory: Object.fromEntries(Object.entries(state.inventory).filter(([id]) => ids.has(id))),
          activity: Object.fromEntries(Object.entries(state.activity).filter(([id]) => ids.has(id))),
        }))
        useWorkspaceConnectionStore.getState().setProfiles(connections.map(c => ({ id: c.id, name: c.name,
          type: c.id === 'local' ? 'local' : 'remote', apiBaseUrl: locationApiBase(c.id),
        })))
        // Inventory cards load independently; an unreachable server must not
        // delay the activity monitor's first reads on healthy connections.
        void Promise.allSettled(connections.map(async connection => {
          let result: DesktopInventory
          try { result = await desktopRequest<DesktopInventory>(`/${connection.id}/inventory`) }
          catch { result = { projects: [], status: 'unavailable' } }
          if (!isChatIdentityCurrent(identity)) return
          set(state => ({ inventory: { ...state.inventory, [connection.id]: result } }))
        }))
      } catch {
        if (isChatIdentityCurrent(identity)) set({ error: 'Could not load connections. Try again.' })
      }
    })()
    directoryPending = pending
    try { await pending } finally { if (directoryPending === pending) directoryPending = undefined }
  },
  refreshActivity: async () => {
    if (!hasDesktopConnections()) return
    const identity = captureChatIdentity()
    if (ownerIdentity !== identity || !useConnectedWorkStore.getState().loaded) await useConnectedWorkStore.getState().refresh()
    if (!isChatIdentityCurrent(identity)) return
    if (activityPending) return activityPending
    const pending = Promise.allSettled(useConnectedWorkStore.getState().connections
      .map(async connection => {
        try {
          let result: HeaderSummaryResponse
          if (connection.id === getRawActiveWorkspaceId()) {
            result = await agentApi.getHeaderSummary()
          } else {
            const response = await fetch(`${locationApiBase(connection.id)}/api/header-summary`, {
              headers: { Authorization: `Bearer ${getAuthToken() || ''}` }, signal: AbortSignal.timeout(15_000),
            })
            if (!response.ok) throw Object.assign(new Error('Activity unavailable'), { status: response.status })
            result = await response.json()
          }
          if (isChatIdentityCurrent(identity)) set(state => ({ activity: { ...state.activity, [connection.id]: {
            sessions: result.active_sessions || [], status: 'connected', updatedAt: Date.now(),
          } } }))
        } catch (error) {
          const code = (error as { status?: number; response?: { status?: number } }).status || (error as { response?: { status?: number } }).response?.status
          const status = code === 401 || code === 403 ? 'sign_in' : 'unavailable'
          if (isChatIdentityCurrent(identity)) set(state => ({ activity: { ...state.activity, [connection.id]: {
            sessions: status === 'sign_in' ? [] : state.activity[connection.id]?.sessions || [], updatedAt: state.activity[connection.id]?.updatedAt, status,
          } } }))
        }
      })).then(() => {})
    activityPending = pending
    try { await pending } finally { if (activityPending === pending) activityPending = undefined }
  },
}))

function locationApiBase(id: string): string {
  return id === 'local' ? getLocalApiBaseUrl() : `${getDesktopApiBaseUrl()}/api/connections/${id}`
}

const noActivity: Record<string, LocationActivity> = {}
const localOnly = [localConnection]
// An account switch may render before the refresh effect runs. Never paint the
// departing account's cached locations/activity during that first frame.
export const connectedLocations = (state: ReturnType<typeof useConnectedWorkStore.getState>) => isChatIdentityCurrent(state.identity) ? state.connections : localOnly
export const connectedActivity = (state: ReturnType<typeof useConnectedWorkStore.getState>) => isChatIdentityCurrent(state.identity) ? state.activity : noActivity
