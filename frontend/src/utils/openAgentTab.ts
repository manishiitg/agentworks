import { markViewSwitchStart } from './chatDeliveryTelemetry'
import { useChatStore } from '../stores/useChatStore'
import { activateTab } from './activateTab'
import { openAppProduct } from './appNavigation'
import { currentProductRoute, productRoutePath } from './productRoutes'

/** A person's agent selection is a history visit; background tab hydration is not. */
export function openAgentTab(tabId: string) {
  const state = useChatStore.getState()
  const tab = state.chatTabs[tabId]
  if (tab && state.activeTabId !== tabId) markViewSwitchStart(tab.metadata?.agentProfileConversationKey)
  const projectId = tab?.metadata?.agentProfileProjectId
  const key = tab?.metadata?.agentProfileConversationKey
  if (!tab || tab.metadata?.agentProfileId !== 'work' || !projectId || !key) return activateTab(tabId)
  // Another person's agent (owner, 10 Nov: clicking it opened nothing). Its key has the same shape as yours (their
  // Main is keyed like your Main), so an ?agent= route would make the Crew screen switch back to your own tab. Open it
  // with the project route and no agent.
  const here = currentProductRoute()
  if (tab.metadata?.agentProfileTabCanWrite === false && here?.surface === 'work' && here.id === projectId) {
    const path = productRoutePath({ ...here, agent: undefined })
    if (path !== window.location.pathname + window.location.search) window.history.pushState({}, '', path)
    return activateTab(tabId)
  }
  const agent = key === projectId ? 'main' : key.startsWith(`${projectId}:chat:`) ? key.slice(`${projectId}:chat:`.length) : undefined
  if (!agent) return activateTab(tabId)
  const current = currentProductRoute()
  if (current?.surface === 'work' && current.id === projectId) {
    const path = productRoutePath({ ...current, agent })
    if (path !== window.location.pathname + window.location.search) window.history.pushState({}, '', path)
    return activateTab(tabId)
  }
  // Do not activate another project's chat while the surface still belongs
  // to the previous project. The admitted route selects both together.
  openAppProduct({ surface: 'work', id: projectId, agent })
  return true
}
