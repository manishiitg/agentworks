import { useEffect } from 'react'
import { create } from 'zustand'
import { useSidebarExpansionStore } from './useSidebarExpansionStore'
import { connectionDisplayName } from '../utils/connectedWorkPresentation'
import { desktopRequest, type DesktopConnection, type DesktopInventory } from '../services/desktopConnections'
import { agentApi, connectionApiBase, getAuthToken } from '../services/api'
import type { ActiveSessionInfo, HeaderSummaryResponse } from '../services/api-types'
import { connectionLiveFeed, liveFeed } from '../services/liveFeed'
import { captureChatIdentity, isChatIdentityCurrent } from '../utils/chatIdentity'
import { getRawActiveWorkspaceId, useWorkspaceConnectionStore } from './useWorkspaceConnectionStore'

export const localConnection: DesktopConnection = { id: 'local', name: 'Local', server: '' }
export const hasDesktopConnections = () => Boolean(window.electronAPI?.getDesktopConnection?.())
export type LocationActivity = { sessions: ActiveSessionInfo[]; status: DesktopInventory['status']; updatedAt?: number }
let directoryPending: Promise<void> | undefined
let activityPending: Promise<void> | undefined
let ownerIdentity = -1
// Screens mount, remount and regain focus often; a refresh that finished moments ago is reused instead of reloading
// every location again (each inventory read costs the server seconds).
const DIRECTORY_FRESH_MS = 30_000
const ACTIVITY_FRESH_MS = 5_000
let directoryAt = 0
let activityAt = 0

type ConnectedWorkSet = (fn: (state: { activity: Record<string, LocationActivity> }) => { activity: Record<string, LocationActivity> }) => void
async function readActivity(id: string, identity: number, set: ConnectedWorkSet) {
  try {
    let result: HeaderSummaryResponse
    if (id === getRawActiveWorkspaceId()) {
      result = await agentApi.getHeaderSummary()
    } else {
      const response = await fetch(`${locationApiBase(id)}/api/header-summary`, {
        headers: { Authorization: `Bearer ${getAuthToken() || ''}` }, signal: AbortSignal.timeout(15_000),
      })
      if (!response.ok) throw Object.assign(new Error('Activity unavailable'), { status: response.status })
      result = await response.json()
    }
    if (isChatIdentityCurrent(identity)) set(state => ({ activity: { ...state.activity, [id]: {
      sessions: result.active_sessions || [], status: 'connected', updatedAt: Date.now(),
    } } }))
  } catch (error) {
    const code = (error as { status?: number; response?: { status?: number } }).status || (error as { response?: { status?: number } }).response?.status
    const status = code === 401 || code === 403 ? 'sign_in' : 'unavailable'
    if (isChatIdentityCurrent(identity)) set(state => ({ activity: { ...state.activity, [id]: {
      sessions: status === 'sign_in' ? [] : state.activity[id]?.sessions || [], updatedAt: state.activity[id]?.updatedAt, status,
    } } }))
  }
}

/** Connection-tagged navigation/activity only. Remote conversations never enter the chat store. */
export const useConnectedWorkStore = create<{
  identity: number
  connections: DesktopConnection[]
  inventory: Record<string, DesktopInventory>
  activity: Record<string, LocationActivity>
  loaded: boolean
  error: string
  /** force: a server was just connected or disconnected; skip the freshness window. */
  refresh: (force?: boolean) => Promise<void>
  /** only: refresh just these connections (the others are kept live by their streams). */
  refreshActivity: (only?: (id: string) => boolean) => Promise<void>
  /** One network's agents changed: read its activity again, ignoring the shared freshness window. */
  refreshConnectionActivity: (id: string) => Promise<void>
}>(set => ({
  identity: captureChatIdentity(), connections: [localConnection], inventory: {}, activity: {}, loaded: false, error: '',
  refresh: async (force = false) => {
    if (!hasDesktopConnections()) return
    const identity = captureChatIdentity()
    if (ownerIdentity !== identity) {
      ownerIdentity = identity
      directoryPending = undefined; activityPending = undefined; directoryAt = 0; activityAt = 0
      set({ identity, connections: [localConnection], inventory: {}, activity: {}, loaded: false, error: '' })
    }
    if (directoryPending) return directoryPending
    if (!force && Date.now() - directoryAt < DIRECTORY_FRESH_MS) return
    directoryAt = Date.now()
    const pending = (async () => {
      try {
        const data = await desktopRequest<{ connections: DesktopConnection[] }>()
        if (!isChatIdentityCurrent(identity)) return
        const connections = [localConnection, ...data.connections.map(connection => ({ ...connection, name: useSidebarExpansionStore.getState().connectionNames[connection.id] || connectionDisplayName(connection) }))]
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
        directoryAt = 0
        if (isChatIdentityCurrent(identity)) set({ error: 'Could not load connections. Try again.' })
      }
    })()
    directoryPending = pending
    try { await pending } finally { if (directoryPending === pending) directoryPending = undefined }
  },
  refreshActivity: async only => {
    if (!hasDesktopConnections()) return
    const identity = captureChatIdentity()
    if (ownerIdentity !== identity || !useConnectedWorkStore.getState().loaded) await useConnectedWorkStore.getState().refresh()
    if (!isChatIdentityCurrent(identity)) return
    if (activityPending) return activityPending
    if (Date.now() - activityAt < ACTIVITY_FRESH_MS) return
    activityAt = Date.now()
    const pending = Promise.allSettled(useConnectedWorkStore.getState().connections
      .filter(connection => !only || only(connection.id))
      .map(connection => readActivity(connection.id, identity, set))).then(() => {})
    activityPending = pending
    try { await pending } finally { if (activityPending === pending) activityPending = undefined }
  },
  refreshConnectionActivity: async id => {
    if (!hasDesktopConnections()) return
    const identity = captureChatIdentity()
    if (ownerIdentity !== identity || !useConnectedWorkStore.getState().connections.some(connection => connection.id === id)) return
    await readActivity(id, identity, set)
  },
}))

// Every screen that shows other networks (menu, activity panel, Home) shares one set of readers. Each network's own
// live stream says when its agents change (owner, 20 Nov, #3383: status on every network shows at once, not after a
// 10 s poll); a network whose stream is down is polled every 10 s, every network again each minute and on focus.
const ACTIVITY_POLL_MS = 10_000
const DIRECTORY_POLL_MS = 60_000
// A busy server sends many "sessions changed" notices: read its activity at most this often.
const LIVE_READ_MIN_MS = 2_000
let pollSubscribers = 0
let stopPolling: (() => void) | undefined
const feedFor = (id: string) => id === getRawActiveWorkspaceId() ? liveFeed : connectionLiveFeed(locationApiBase(id))

/** Keep one live stream open per connected network while any screen shows them; returns a stop function. */
function watchConnectionStreams(): () => void {
  const streams = new Map<string, () => void>()
  const timers = new Map<string, number>()
  const lastRead = new Map<string, number>()
  const read = (id: string) => {
    timers.delete(id)
    if (document.hidden) return // focus reads every network again
    lastRead.set(id, Date.now())
    void useConnectedWorkStore.getState().refreshConnectionActivity(id)
  }
  const changed = (id: string) => () => {
    if (timers.has(id)) return
    timers.set(id, window.setTimeout(() => read(id), Math.max(0, (lastRead.get(id) ?? 0) + LIVE_READ_MIN_MS - Date.now())))
  }
  const sync = () => {
    const ids = new Set(connectedLocations(useConnectedWorkStore.getState()).map(connection => connection.id))
    for (const [id, stop] of streams) if (!ids.has(id)) { stop(); streams.delete(id); window.clearTimeout(timers.get(id)); timers.delete(id) }
    for (const id of ids) if (!streams.has(id)) streams.set(id, feedFor(id).subscribe(['sessions'], null, changed(id)))
  }
  sync()
  const unsubscribe = useConnectedWorkStore.subscribe((state, previous) => { if (state.connections !== previous.connections) sync() })
  return () => {
    unsubscribe()
    streams.forEach(stop => stop()); streams.clear()
    timers.forEach(timer => window.clearTimeout(timer)); timers.clear()
  }
}

export function useConnectedWorkPolling(enabled = true) {
  useEffect(() => {
    if (!enabled || !hasDesktopConnections()) return
    const store = useConnectedWorkStore.getState
    if (pollSubscribers++ === 0) {
      const notLive = (id: string) => feedFor(id).getStatus() !== 'live'
      const activity = () => { if (!document.hidden) void store().refreshActivity(notLive) }
      const directory = () => { if (!document.hidden) { void store().refresh(); void store().refreshActivity() } }
      const focus = () => { void store().refresh(); void store().refreshActivity() }
      void store().refresh().then(() => store().refreshActivity())
      const stopStreams = watchConnectionStreams()
      const timers = [window.setInterval(activity, ACTIVITY_POLL_MS), window.setInterval(directory, DIRECTORY_POLL_MS)]
      window.addEventListener('focus', focus)
      stopPolling = () => { stopStreams(); timers.forEach(window.clearInterval); window.removeEventListener('focus', focus) }
    }
    return () => { if (--pollSubscribers === 0) { stopPolling?.(); stopPolling = undefined } }
  }, [enabled])
}

export const locationApiBase = connectionApiBase

const noActivity: Record<string, LocationActivity> = {}
const localOnly = [localConnection]
// An account switch may render before the refresh effect runs. Never paint the
// departing account's cached locations/activity during that first frame.
export const connectedLocations = (state: ReturnType<typeof useConnectedWorkStore.getState>) => isChatIdentityCurrent(state.identity) ? state.connections : localOnly
export const connectedActivity = (state: ReturnType<typeof useConnectedWorkStore.getState>) => isChatIdentityCurrent(state.identity) ? state.activity : noActivity
