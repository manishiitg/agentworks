import { useEffect, useRef, useState } from 'react'
import api from '../../services/api'
import { usedFeatures } from '../../utils/featureUsage'
import { useProductSurfaceStore } from '../../stores/useProductSurfaceStore'
import { useChatStore } from '../../stores/useChatStore'

// Only after the agent has been working this long: never on quick turns.
export const WORKING_TIP_DELAY_MS = 10_000

/** Chat tabs open in the active Code project (main chat plus side chats). */
function activeCodeProjectTabs(): number {
  const { chatTabs, activeTabId } = useChatStore.getState()
  const key = activeTabId ? chatTabs[activeTabId]?.metadata?.agentProfileConversationKey : undefined
  if (!key) return 1
  const project = key.split(':')[0]
  return Object.values(chatTabs).filter(tab => {
    const other = tab.metadata?.agentProfileConversationKey
    return other === project || other?.startsWith(project + ':chat:')
  }).length || 1
}

/**
 * The server's working tip for this turn, asked for once after ten seconds of work. The server chooses it (eligibility,
 * what this person has seen, a cooldown, their on/off setting), records it as shown and gives the same tip again if
 * the same turn asks twice. The browser only reports the facts it alone has (open Code tabs, features already used)
 * and shows the answer.
 */
export function useRareWorkingTip(working: boolean, turnKey?: string | number): string | null {
  const surface = useProductSurfaceStore(state => state.productSurface)
  const [tip, setTip] = useState<string | null>(null)
  const fallbackTurn = useRef(`mount-${Math.random().toString(36).slice(2)}`)
  const turn = String(turnKey ?? fallbackTurn.current)
  useEffect(() => {
    setTip(null)
    if (!working) return
    let stopped = false
    const controller = new AbortController()
    const timer = window.setTimeout(async () => {
      try {
        const response = await api.get<{ tip: { id: string; text: string } | null }>('/api/product-tips/next', {
          params: { surface, code_tabs: surface === 'work' ? activeCodeProjectTabs() : 1, used: usedFeatures().join(','), turn },
          skipSessionContext: true, timeout: 5000, signal: controller.signal,
        })
        if (!stopped) setTip(response.data.tip?.text ?? null)
      } catch { /* a tip is never worth an error on screen */ }
    }, WORKING_TIP_DELAY_MS)
    return () => { stopped = true; window.clearTimeout(timer); controller.abort() }
  }, [working, surface, turn])
  return working ? tip : null
}
