import { useCallback, useEffect, useRef, useState } from 'react'
import { agentApi } from '../services/api'
import type { SessionBackgroundWorkItem } from '../services/api-types'
import { useChatStore } from '../stores/useChatStore'
import { captureChatIdentity, isChatIdentityCurrent } from '../utils/chatIdentity'
import { runtimeHasBackgroundAgents } from '../utils/runtimeActivity'

/** Keep the activity count and task actions alive while the menu is closed. */
export function useSessionBackgroundWork(tabId: string | null | undefined, open: boolean) {
  const identity = captureChatIdentity()
  const sessionId = useChatStore(state => tabId ? state.chatTabs[tabId]?.sessionId ?? null : null)
  const tabHasBg = useChatStore(state => tabId ? state.chatTabs[tabId]?.hasRunningBgAgents ?? false : false)
  const sessionHasBg = useChatStore(state => sessionId
    ? runtimeHasBackgroundAgents(state.activeSessionsCache.find(item => item.session_id === sessionId)) : false)
  const active = !!sessionId && (tabHasBg || sessionHasBg)
  const scope = `${identity}:${tabId}:${sessionId}`
  const currentScope = useRef(scope)
  currentScope.current = scope
  const [snapshot, setSnapshot] = useState({ scope, items: [] as SessionBackgroundWorkItem[] })
  const [stopping, setStopping] = useState<Record<string, boolean>>({})
  const inFlight = useRef(new Set<string>())
  const requestVersion = useRef(0)
  const [now, setNow] = useState(() => Date.now())
  const owns = useCallback(() => currentScope.current === scope && isChatIdentityCurrent(identity) && !!tabId &&
    useChatStore.getState().getTab(tabId)?.sessionId === sessionId, [identity, tabId, sessionId, scope])

  const refresh = useCallback(async () => {
    if (!active || !sessionId || !owns()) return
    const version = ++requestVersion.current
    try {
      const response = await agentApi.getSessionBackgroundWork(sessionId)
      if (version === requestVersion.current && owns()) setSnapshot({ scope, items: response.items ?? [] })
    } catch { /* Keep the last known tasks visible during a transient failure. */ }
  }, [active, sessionId, owns, scope])

  useEffect(() => {
    if (!active) { setSnapshot({ scope, items: [] }); return }
    void refresh()
    const timer = setInterval(() => { void refresh() }, open ? 3000 : 5000)
    return () => { clearInterval(timer); requestVersion.current += 1 }
  }, [active, open, refresh, scope])

  useEffect(() => {
    if (!active || !open) return
    setNow(Date.now())
    const timer = setInterval(() => setNow(Date.now()), 1000)
    return () => clearInterval(timer)
  }, [active, open])

  const stopItem = async (item: SessionBackgroundWorkItem) => {
    const key = `${scope}:${item.id}`
    if (!sessionId || !item.can_stop || !owns() || inFlight.current.has(key)) return
    inFlight.current.add(key)
    setStopping(prev => ({ ...prev, [key]: true }))
    try {
      await agentApi.stopSessionBackgroundWork(sessionId, item.id)
      if (owns()) {
        requestVersion.current += 1
        setSnapshot(prev => prev.scope === scope
          ? { scope, items: prev.items.filter(existing => existing.id !== item.id) } : prev)
      }
    } catch {
      if (owns()) useChatStore.getState().addToast(`Could not stop ${item.label}.`, 'error')
    } finally {
      inFlight.current.delete(key)
      setStopping(prev => { const next = { ...prev }; delete next[key]; return next })
      if (owns()) void refresh()
    }
  }

  return {
    active,
    items: active && snapshot.scope === scope ? snapshot.items : [],
    now,
    isStopping: (item: SessionBackgroundWorkItem) => Boolean(stopping[`${scope}:${item.id}`]),
    stopItem,
  }
}
