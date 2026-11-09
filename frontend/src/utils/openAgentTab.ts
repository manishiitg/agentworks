import { useChatStore } from '../stores/useChatStore'
import { activateTab } from './activateTab'
import { openAppProduct } from './appNavigation'
import { currentProductRoute, productRoutePath } from './productRoutes'

/** A person's agent selection is a history visit; background tab hydration is not. */
export function openAgentTab(tabId: string) {
  const tab = useChatStore.getState().chatTabs[tabId]
  const projectId = tab?.metadata?.agentProfileProjectId
  const key = tab?.metadata?.agentProfileConversationKey
  if (!tab || tab.metadata?.agentProfileId !== 'work' || !projectId || !key) return activateTab(tabId)
  const agent = key === projectId ? 'main' : key.startsWith(`${projectId}:chat:`) ? key.slice(`${projectId}:chat:`.length) : undefined
  if (!agent) return activateTab(tabId)
  const current = currentProductRoute()
  if (current?.surface === 'work' && current.id === projectId) {
    const path = productRoutePath({ ...current, agent })
    if (path !== window.location.pathname + window.location.search) window.history.pushState({}, '', path)
    return activateTab(tabId)
  }
  const activated = activateTab(tabId)
  openAppProduct({ surface: 'work', id: projectId, agent })
  return activated
}
