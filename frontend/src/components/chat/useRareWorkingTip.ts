import { useEffect, useState } from 'react'
import { claimWorkingTip } from '../../products/productTips'
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

/** A very rare product hint beside "Working…": at most one a day, chosen for
 * this product and moment, never for a feature already used. */
export function useRareWorkingTip(working: boolean): string | null {
  const surface = useProductSurfaceStore(state => state.productSurface)
  const [tip, setTip] = useState<string | null>(null)
  useEffect(() => {
    if (!working) { setTip(null); return }
    const timer = window.setTimeout(() => {
      setTip(claimWorkingTip({ surface, codeTabs: surface === 'code' ? activeCodeProjectTabs() : 1 }))
    }, WORKING_TIP_DELAY_MS)
    return () => window.clearTimeout(timer)
  }, [working, surface])
  return working ? tip : null
}
