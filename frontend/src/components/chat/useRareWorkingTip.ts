import { useEffect, useState } from 'react'
import { claimWorkingTip, hasUnseenEligibleTip, workingTipDelayMs, type ProductTipCatalog } from '../../products/productTips'
import api from '../../services/api'
import { useAuthStore } from '../../stores/useAuthStore'
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

/** YAML-defined hints beside Working, slowing down as this viewer learns the product. */
export function useRareWorkingTip(working: boolean): string | null {
  const surface = useProductSurfaceStore(state => state.productSurface)
  const userId = useAuthStore(state => state.user?.id)
  const [tip, setTip] = useState<string | null>(null)
  useEffect(() => {
    setTip(null)
    if (!working) return
    let stopped = false
    let timer: number
    const controller = new AbortController()
    const context = () => ({ surface, userId, codeTabs: surface === 'code' ? activeCodeProjectTabs() : 1 })
    const schedule = () => { timer = window.setTimeout(() => { void showTip() }, Math.max(WORKING_TIP_DELAY_MS, workingTipDelayMs(context()))) }
    const showTip = async () => {
      try {
        const response = await api.get<{ tips: ProductTipCatalog }>('/api/product-tips', { skipSessionContext: true, timeout: 5000, signal: controller.signal })
        if (stopped) return
        const definitions = response.data.tips[surface] || []
        const next = claimWorkingTip(context(), Date.now(), definitions)
        if (!next) {
          // Another chat may have claimed this slot, moving our due time
          // forward: retry then while unseen eligible tips remain. Two tabs
          // can still claim at once; localStorage is not atomic.
          if (hasUnseenEligibleTip(context(), definitions)) schedule()
          return
        }
        setTip(next)
        schedule()
      } catch {
        if (!stopped) timer = window.setTimeout(() => { void showTip() }, 60_000)
      }
    }
    schedule()
    return () => { stopped = true; window.clearTimeout(timer); controller.abort() }
  }, [working, surface, userId])
  return working ? tip : null
}
