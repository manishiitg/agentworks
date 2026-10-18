import { useCallback, useEffect } from 'react'
import { create } from 'zustand'
import { contextShortcutsApi, type ContextShortcutPreference, type ShortcutProduct } from '../services/contextShortcuts'
import { useAuthStore } from '../stores/useAuthStore'
import { useWorkspaceConnectionStore } from '../stores/useWorkspaceConnectionStore'
import { getApiBaseUrl } from '../services/api'
import { captureChatIdentity, isChatIdentityCurrent } from '../utils/chatIdentity'

type Entry = { status: 'loading' | 'ready' | 'error'; record?: ContextShortcutPreference; error?: string; request: number }
export const useContextShortcutStore = create<{ entries: Record<string, Entry> }>(() => ({ entries: {} }))
let requestID = 0

export const DEFAULT_CONTEXT_SHORTCUTS: Record<ShortcutProduct, readonly string[]> = {
  agentworks: ['report', 'workshop:schedules', 'knowledge', 'pulse', 'human-actions', 'browser', 'workshop:chats', 'mcp'],
  relays: ['flow', 'report', 'workshop:triggers', 'workshop:conditions', 'costs', 'execution-logs', 'files', 'workshop:chats', 'mcp'],
  work: ['dashboard', 'schedules', 'memory', 'suggestions', 'browser', 'schedules:chats', 'mcp'],
  code: ['dashboard', 'schedules', 'memory', 'files', 'browser', 'schedules:chats', 'mcp'],
}

export function useContextShortcuts(product: ShortcutProduct, legacy?: ShortcutProduct) {
  const account = useAuthStore(state => state.user?.id)
  const workspace = useWorkspaceConnectionStore(state => state.activeWorkspaceId)
  const endpoint = account ? getApiBaseUrl() : ''
  const scope = JSON.stringify([workspace, endpoint, account, product, legacy])
  const entry = useContextShortcutStore(state => state.entries[scope])
  const currentScope = useCallback(() => useAuthStore.getState().user?.id === account && useWorkspaceConnectionStore.getState().activeWorkspaceId === workspace && getApiBaseUrl() === endpoint, [account, workspace, endpoint])
  const update = useCallback((next: Entry) => useContextShortcutStore.setState(state => ({ entries: { ...state.entries, [scope]: next } })), [scope])
  const load = useCallback(async (signal?: AbortSignal) => {
    if (!account || !currentScope()) return undefined
    const identity = captureChatIdentity(), request = ++requestID
    update({ ...useContextShortcutStore.getState().entries[scope], status: 'loading', request, error: undefined })
    try {
      const record = await contextShortcutsApi.get(product, legacy, signal)
      if (signal?.aborted || !isChatIdentityCurrent(identity) || !currentScope() || useContextShortcutStore.getState().entries[scope]?.request !== request) return undefined
      update({ record, status: 'ready', request })
      return record
    } catch {
      if (!signal?.aborted && isChatIdentityCurrent(identity) && currentScope() && useContextShortcutStore.getState().entries[scope]?.request === request)
        update({ ...useContextShortcutStore.getState().entries[scope], status: 'error', request, error: 'Could not load your shortcuts.' })
      return undefined
    }
  }, [account, currentScope, scope, product, legacy, update])
  useEffect(() => { const controller = new AbortController(); void load(controller.signal); return () => controller.abort() }, [load])
  const save = useCallback(async (shortcuts: string[] | null) => {
    const previous = useContextShortcutStore.getState().entries[scope]
    if (!account || !currentScope() || previous?.status !== 'ready' || !previous.record) throw new Error('Reload your shortcuts before saving.')
    const identity = captureChatIdentity(), request = ++requestID
    update({ ...previous, request })
    try {
      const record = await contextShortcutsApi.save(product, { ...previous.record, shortcuts })
      if (!currentScope() || !isChatIdentityCurrent(identity) || useContextShortcutStore.getState().entries[scope]?.request !== request) throw new Error('The account or product changed.')
      update({ record, status: 'ready', request })
      return record
    } catch (error) {
      const status = (error as { response?: { status?: number } })?.response?.status
      throw new Error(status === 409 ? 'Your shortcuts changed in another tab. Reload before saving.' : status === 403 ? 'This product is no longer available.' : 'Could not save your shortcuts. Try again.')
    }
  }, [scope, account, currentScope, product, update])
  return { scope, entry, load, save, available: Boolean(account) }
}
