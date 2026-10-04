import type { ChatTab, EventViewMode } from '../stores/useChatStore'
import { useChatStore } from '../stores/useChatStore'
import { useGlobalPresetStore } from '../stores/useGlobalPresetStore'
import { useWorkflowStore } from '../stores/useWorkflowStore'
import { activateTab } from './activateTab'
import { selectWorkflowPreset } from './workflowNavigation'

/** Asks the active chat composer to take focus with the caret at the end. */
export const CHAT_FOCUS_COMPOSER_EVENT = 'chat-focus-composer'

function normalizeWorkspacePath(value?: string | null): string {
  return (value || '').trim().replace(/^\/+|\/+$/g, '').toLowerCase()
}

function tabRecency(tab: ChatTab): number {
  return tab.lastAccessedAt ?? tab.createdAt ?? 0
}

function isInteractiveWorkflowTab(tab: ChatTab, presetId: string): boolean {
  return tab.metadata?.mode === 'workflow' &&
    tab.metadata?.presetQueryId === presetId &&
    tab.metadata?.isViewOnly !== true &&
    tab.metadata?.isScheduledRun !== true &&
    tab.metadata?.isBotRun !== true
}

function isInteractiveProductTab(tab: ChatTab, profileId: string, conversationKey: string): boolean {
  return tab.metadata?.mode === 'multi-agent' &&
    tab.metadata?.agentProfileId === profileId &&
    tab.metadata?.agentProfileConversationKey === conversationKey &&
    tab.metadata?.agentProfileBuilder !== true &&
    tab.metadata?.isViewOnly !== true &&
    tab.metadata?.isScheduledRun !== true &&
    tab.metadata?.isBotRun !== true
}

/** Resolve a product pane through its durable identity, never a retained tab id. */
export function selectWorkspacePaneProductTab(
  tabs: Record<string, ChatTab>,
  profileId: string,
  conversationKey: string,
  activeTabId?: string | null,
): ChatTab | undefined {
  return Object.values(tabs)
    .filter(tab => isInteractiveProductTab(tab, profileId, conversationKey))
    .sort((left, right) => {
      if ((left.tabId === activeTabId) !== (right.tabId === activeTabId)) {
        return left.tabId === activeTabId ? -1 : 1
      }
      return tabRecency(right) - tabRecency(left)
    })[0]
}

/** Select the interactive workflow conversation used by every right-pane action. */
export function selectWorkspacePaneWorkflowTab(
  tabs: Record<string, ChatTab>,
  presetId: string,
  activeTabId?: string | null,
): ChatTab | undefined {
  const candidates = Object.values(tabs).filter(tab => isInteractiveWorkflowTab(tab, presetId))

  return candidates.sort((left, right) => {
    if (left.isStreaming !== right.isStreaming) return left.isStreaming ? 1 : -1
    if ((left.tabId === activeTabId) !== (right.tabId === activeTabId)) {
      return left.tabId === activeTabId ? -1 : 1
    }
    const leftBuilder = left.metadata?.phaseId === 'workflow-builder'
    const rightBuilder = right.metadata?.phaseId === 'workflow-builder'
    if (leftBuilder !== rightBuilder) return leftBuilder ? -1 : 1
    return tabRecency(right) - tabRecency(left)
  })[0]
}

async function findWorkflowPreset(workspacePath: string) {
  const find = () => {
    const normalizedTarget = normalizeWorkspacePath(workspacePath)
    return useGlobalPresetStore.getState().workflowPresets.find(preset =>
      normalizeWorkspacePath(preset.selectedFolder?.filepath) === normalizedTarget)
  }

  let preset = find()
  if (!preset) {
    await useGlobalPresetStore.getState().refreshPresets()
    preset = find()
  }
  return preset
}

export type WorkspacePaneChatResult = {
  tabId: string
  reused: boolean
  queuedBehindRunningTurn: boolean
}

type WorkspacePaneChatRequest = {
  notificationId?: string
  message: string
  viewMode?: EventViewMode
} & (
  | { workspacePath: string; tabId?: never; profileId?: never; conversationKey?: never }
  | { tabId: string; workspacePath?: never; profileId?: never; conversationKey?: never }
  | { profileId: string; conversationKey: string; tabId?: never; workspacePath?: never }
)

/**
 * The single entry point for messages originating in a right-side pane.
 * Dashboard HTML, human decisions, Ask AI, Pulse, and Work-project panes all
 * resolve their destination here and then use the same durable chat queue.
 */
async function resolveWorkspacePaneChatTab(request: WorkspacePaneChatRequest): Promise<{ tabId: string; targetTab: ChatTab; reused: boolean }> {
  const workspacePath = 'workspacePath' in request ? request.workspacePath : undefined
  const requestedTabId = 'tabId' in request ? request.tabId : undefined
  const profileId = 'profileId' in request ? request.profileId : undefined
  const conversationKey = 'conversationKey' in request ? request.conversationKey : undefined
  let targetTab: ChatTab | undefined
  let tabId: string
  let reused = false

  if (profileId && conversationKey) {
    const chatStore = useChatStore.getState()
    targetTab = selectWorkspacePaneProductTab(chatStore.chatTabs, profileId, conversationKey, chatStore.activeTabId)
    if (!targetTab) throw new Error('This project chat is not ready. Reopen the project and try again.')
    tabId = targetTab.tabId
    reused = true
  } else if (requestedTabId) {
    tabId = requestedTabId
    targetTab = useChatStore.getState().getTab(tabId)
    reused = true
    if (!targetTab) throw new Error('This chat is not available.')
  } else {
    if (!workspacePath) throw new Error('An automation path or project conversation is required.')
    const preset = await findWorkflowPreset(workspacePath)
    if (!preset) throw new Error(`Could not find the automation for ${workspacePath}.`)
    if (!selectWorkflowPreset(preset)) throw new Error('Failed to open the automation.')

    const chatStore = useChatStore.getState()
    targetTab = selectWorkspacePaneWorkflowTab(chatStore.chatTabs, preset.id, chatStore.activeTabId)
    if (targetTab) {
      tabId = targetTab.tabId
      reused = true
    } else {
      tabId = await chatStore.createChatTab('Automation Builder', {
        mode: 'workflow',
        phaseId: 'workflow-builder',
        phaseName: 'Automation Builder',
        presetQueryId: preset.id,
      })
      targetTab = useChatStore.getState().getTab(tabId)
    }
    if (!targetTab) throw new Error('Failed to open a chat for this message.')
  }
  return { tabId, targetTab, reused }
}

export async function sendWorkspacePaneMessageToChat(request: WorkspacePaneChatRequest): Promise<WorkspacePaneChatResult> {
  const { message, viewMode = 'formatted' } = request
  if (!message.trim()) throw new Error('Write a message before opening chat.')
  const { tabId, targetTab, reused } = await resolveWorkspacePaneChatTab(request)

  const queuedBehindRunningTurn = targetTab.isStreaming
  const chatStore = useChatStore.getState()
  const config = chatStore.getTabConfig(tabId)
  if (request.notificationId && config?.mcpOAuthNotificationIDs?.includes(request.notificationId)) {
    return { tabId, reused, queuedBehindRunningTurn }
  }
  const existingQueue = config?.queuedMessages || []
  chatStore.setTabConfig(tabId, {
    queuedMessages: [...existingQueue, message],
    ...(request.notificationId ? { mcpOAuthNotificationIDs: [...(config?.mcpOAuthNotificationIDs ?? []), request.notificationId].slice(-100) } : {}),
  })
  chatStore.setTabViewMode(tabId, viewMode)
  chatStore.setAutoScroll(true)
  activateTab(tabId)

  if (queuedBehindRunningTurn) {
    void chatStore.getActiveSessions(true).catch(error => {
      console.warn('[WorkspacePaneChat] Failed to refresh the target chat session', error)
    })
  }

  if (targetTab.metadata?.mode === 'workflow') {
    const workflowStore = useWorkflowStore.getState()
    workflowStore.setShowChatArea(true)
    workflowStore.setShowWorkspacePane(true)
    workflowStore.setFocusedPane('chat')
  }

  window.setTimeout(() => window.dispatchEvent(new CustomEvent('chat-scroll-to-bottom')), 50)
  return { tabId, reused, queuedBehindRunningTurn }
}

/**
 * Opens the pane's chat with text placed in the composer for the user to
 * finish and send, instead of sending it. An existing unsent draft is kept
 * below the new text.
 */
export async function openWorkspacePaneChatWithDraft(request: WorkspacePaneChatRequest): Promise<{ tabId: string }> {
  const draft = request.message.trim()
  if (!draft) throw new Error('Nothing to put in the chat.')
  const { tabId, targetTab } = await resolveWorkspacePaneChatTab(request)
  const chatStore = useChatStore.getState()
  const existing = (chatStore.getTabConfig(tabId)?.inputText || '').trim()
  chatStore.setTabConfig(tabId, { inputText: existing ? `${request.message}\n\n${existing}` : request.message })
  activateTab(tabId)
  if (targetTab.metadata?.mode === 'workflow') {
    const workflowStore = useWorkflowStore.getState()
    workflowStore.setShowChatArea(true)
    workflowStore.setShowWorkspacePane(true)
    workflowStore.setFocusedPane('chat')
  }
  window.setTimeout(() => window.dispatchEvent(new CustomEvent(CHAT_FOCUS_COMPOSER_EVENT)), 50)
  return { tabId }
}
