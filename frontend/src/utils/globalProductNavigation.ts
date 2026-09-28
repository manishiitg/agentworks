import type { ActiveSessionInfo } from '../services/api-types'
import { useChatStore, type ChatTab } from '../stores/useChatStore'
import { useProductSurfaceStore } from '../stores/useProductSurfaceStore'
import { activateTab } from './activateTab'
import { isScheduledSession } from './workflowSessionKinds'
import { isScheduledWorkflowSession, openCanonicalActivitySession } from './workflowSessionRestore'

const normalizedPath = (value?: string | null): string => (value || '')
  .trim()
  .replace(/\\/g, '/')
  .replace(/^\/+|\/+$/g, '')

export function isWorkProductSession(session: Pick<ActiveSessionInfo, 'session_id' | 'workspace_path'>): boolean {
  if (session.session_id.startsWith('work:project:')) return true
  return /(?:^|\/)Chats\/Work\/projects\/[^/]+(?:\/|$)/i.test(normalizedPath(session.workspace_path))
}

export function workProjectIdForTab(tab?: Pick<ChatTab, 'metadata'> | null): string | null {
  const explicit = tab?.metadata?.agentProfileProjectId?.trim()
  if (explicit) return explicit
  const key = tab?.metadata?.agentProfileConversationKey?.trim()
  if (!key) return null
  return key.split(':')[0]?.trim() || null
}

export function workProjectIdForSession(
  session: Pick<ActiveSessionInfo, 'session_id' | 'preset_query_id'>,
  tab?: Pick<ChatTab, 'metadata'> | null,
): string | null {
  const fromTab = workProjectIdForTab(tab)
  if (fromTab) return fromTab
  const preset = session.preset_query_id?.trim()
  if (preset) return preset
  return session.session_id.startsWith('work:project:')
    ? session.session_id.slice('work:project:'.length).split(':')[0]?.trim() || null
    : null
}

export function openGlobalTab(tabId: string): boolean {
  const tab = useChatStore.getState().chatTabs[tabId]
  if (!tab) return false
  const surfaces = useProductSurfaceStore.getState()
  if (tab.metadata?.agentProfileId === 'work') {
    surfaces.setSelectedWorkProjectId(workProjectIdForTab(tab))
    surfaces.setProductSurface('work')
  } else {
    surfaces.setProductSurface('agentworks')
  }
  return activateTab(tabId)
}

export async function openGlobalActivitySession(
  session: ActiveSessionInfo,
  options: { title?: string; source?: string } = {},
): Promise<void> {
  try {
    await openGlobalActivitySessionInner(session, options)
  } catch (error) {
    // Both global entry points (header monitor, Ctrl+K) await this without
    // their own recovery. A failed open must toast, never reject unhandled
    // after half-switching the surface.
    console.error('[GlobalActivity] failed to open session', session.session_id, error)
    useChatStore.getState().addToast('Could not open this activity.', 'error')
  }
}

async function openGlobalActivitySessionInner(
  session: ActiveSessionInfo,
  options: { title?: string; source?: string },
): Promise<void> {
  const scheduled = isScheduledSession({ sessionId: session.session_id, triggeredBy: session.triggered_by })
  const workflowLane = (session.agent_mode || '').toLowerCase().includes('workflow')
  // Crew trigger/schedule runs are work-product sessions on an isolated
  // conversation with no workflow preset. They open their own read-only run
  // tab — the crew's interactive chat never shows a trigger transcript, so
  // routing the pill there lands the user on their own chat instead.
  if (scheduled && !workflowLane && isWorkProductSession(session)) {
    const { openWorkAutomationRunChat } = await import('../products/work/workAutomationRunRestore')
    await openWorkAutomationRunChat(session, { title: options.title })
    return
  }
  // Trigger runs always open their read-only run tab — even when the run
  // belongs to a crew project. The Work surface shows the crew's interactive
  // chat, never a trigger transcript; routing a schedule pill there lands the
  // user on their own chat instead of the run they clicked.
  if (isScheduledWorkflowSession(session)) {
    useProductSurfaceStore.getState().setProductSurface('agentworks')
    await openCanonicalActivitySession(session, options)
    return
  }
  const chatStore = useChatStore.getState()
  const tab = Object.values(chatStore.chatTabs).find(candidate => candidate.sessionId === session.session_id)
  if (tab?.metadata?.agentProfileId === 'work' || isWorkProductSession(session)) {
    const surfaces = useProductSurfaceStore.getState()
    surfaces.setSelectedWorkProjectId(workProjectIdForSession(session, tab))
    if (!tab) {
      surfaces.setPendingWorkView(session.bot_platform ? 'bots' : session.triggered_by ? 'schedules' : null)
    }
    surfaces.setProductSurface('work')
    if (tab) activateTab(tab.tabId)
    return
  }
  useProductSurfaceStore.getState().setProductSurface('agentworks')
  await openCanonicalActivitySession(session, options)
}
