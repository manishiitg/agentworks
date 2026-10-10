import { useCallback } from 'react'
import type { ChatHistorySession } from '../services/api-types'
import { agentApi } from '../services/api'
import { useChatStore } from '../stores/useChatStore'
import {
  chatHistoryConversationPath,
  chatHistoryRuntimeLabel,
  chatHistorySupportsNativeResume,
  chatHistoryUsesTerminalRestore,
  chatHistoryWorkshopModeLabel,
} from '../components/PreviousChatHistoryPanel'
import { chatHistorySessionTitle } from '../utils/chatHistoryTitle'
import { hydrateTabEvents } from '../utils/sessionRestore'
import { hydrateExecutionConversation } from '../utils/executionConversationRestore'
import { isScheduledChatHistorySession } from '../utils/chatHistoryOpenDisposition'
import { startRestoredTransportTerminal } from '../utils/restoredTerminal'
import { WORK_SIDE_CHAT_LIMIT, isWorkSideChatTab, workSideChatKey } from '../products/work/workTabs'

/**
 * Shared resume handler for product-owned chat history.
 *
 * Product surfaces create and own their profile-bound tab before rendering
 * ChatArea. This handler only attaches history to that existing tab; it must
 * never manufacture the removed profile-less AgentWorks Chat. It mirrors the
 * workflow builder's resume flow: attach the prior
 * conversation (native CLI resume / tmux terminal restore, or a file-context
 * fallback) so the next turn continues that chat.
 *
 * Unlike the empty-landing case, this also resets the current tab when it
 * already has a conversation, so resuming from history mid-chat gives a clean
 * slate that loads the selected chat instead of mixing two conversations.
 */
export function useResumePreviousChat() {
  return useCallback(async (session: ChatHistorySession) => {
    const isExecutionHistory = Boolean(session.bot_platform) ||
      isScheduledChatHistorySession(session) || session.session_id.includes(':trigger:')
    const chatStore = useChatStore.getState()
    let targetTabId = chatStore.activeTabId || undefined
    let targetTab = targetTabId ? chatStore.chatTabs[targetTabId] : undefined

    if (!targetTabId || targetTab?.metadata?.mode !== 'multi-agent' || !targetTab.metadata.agentProfileId) {
      useChatStore.getState().addToast('Open the product chat before resuming its history.', 'error')
      return
    }

    // Crew owns one permanent interactive conversation. Automation runs and
    // older conversations are references to that project, not replacements
    // for its main chat. Open them in a separate read-only tab so selecting a
    // history row can never rebind the canonical project conversation.
    if (targetTab.metadata.agentProfileId === 'work') {
      const projectId = targetTab.metadata.agentProfileProjectId
      const canonical = Object.values(chatStore.chatTabs).find(tab =>
        tab.metadata?.agentProfileId === 'work' &&
        tab.metadata?.agentProfileProjectId === projectId &&
        tab.metadata?.agentProfileConversationKey === projectId &&
        tab.metadata?.isViewOnly !== true,
      )
      // A stale history list may still contain the permanent Crew session.
      // Selecting it means "return to Chat", never "open Chat as history".
      if (canonical?.sessionId === session.session_id) {
        chatStore.switchTab(canonical.tabId)
        return
      }
      const existing = Object.values(chatStore.chatTabs).find(tab =>
        tab.sessionId === session.session_id &&
        tab.metadata?.agentProfileId === 'work' &&
        tab.metadata?.agentProfileProjectId === projectId &&
        tab.metadata?.isViewOnly === true,
      )
      const historyTabId = existing?.tabId || await chatStore.createChatTab(chatHistorySessionTitle(session), {
        ...targetTab.metadata,
        agentProfileBuilder: false,
        agentProfileConversationKey: projectId
          ? `${projectId}:history:${session.session_id}`
          : targetTab.metadata.agentProfileConversationKey,
        agentProfileConversationId: undefined,
        agentProfileRuntimeDirty: false,
        isViewOnly: true,
        isExecutionRun: isExecutionHistory,
        isBotRun: Boolean(session.bot_platform),
        botPlatform: session.bot_platform,
        readOnlyRestoredAt: Date.now(),
        userInteractiveContinuation: false,
      }, session.session_id)
      if (existing) chatStore.setTabMetadata(historyTabId, { isExecutionRun: isExecutionHistory })
      chatStore.setTabCanSteer(historyTabId, false)
      try {
        const workspacePath = session.workspace_path || targetTab.metadata.agentProfileWorkspace
        const runtime = isExecutionHistory
          ? await hydrateExecutionConversation(session.session_id, workspacePath)
          : await hydrateTabEvents(session.session_id, { workspacePath })
        chatStore.setTabStreaming(historyTabId, runtime.status === 'running')
        chatStore.setTabCompleted(historyTabId, runtime.status !== 'running')
        chatStore.setTabViewMode(historyTabId, 'formatted')
        chatStore.switchTab(historyTabId)
      } catch {
        chatStore.addToast('Failed to open the saved conversation', 'error')
      }
      return
    }

    if (session.can_resume === false) {
      const profileId = targetTab.metadata.agentProfileId
      const existing = Object.values(chatStore.chatTabs).find(tab =>
        tab.sessionId === session.session_id && tab.metadata?.isViewOnly === true &&
        tab.metadata?.agentProfileId === profileId,
      )
      const readOnlyTabId = existing?.tabId || await chatStore.createChatTab(chatHistorySessionTitle(session), {
        ...targetTab.metadata,
        agentProfileBuilder: false,
        isViewOnly: true,
        isExecutionRun: isExecutionHistory,
        isBotRun: Boolean(session.bot_platform),
        botPlatform: session.bot_platform,
        readOnlyRestoredAt: Date.now(),
        userInteractiveContinuation: false,
      }, session.session_id)
      if (existing) chatStore.setTabMetadata(readOnlyTabId, { isExecutionRun: isExecutionHistory })
      chatStore.setTabCanSteer(readOnlyTabId, false)
      try {
        const runtime = isExecutionHistory
          ? await hydrateExecutionConversation(session.session_id, session.workspace_path)
          : await hydrateTabEvents(session.session_id, { workspacePath: session.workspace_path })
        chatStore.setTabStreaming(readOnlyTabId, runtime.status === 'running')
        chatStore.setTabCompleted(readOnlyTabId, runtime.status !== 'running')
        chatStore.switchTab(readOnlyTabId)
      } catch {
        chatStore.addToast('Failed to open the saved conversation', 'error')
      }
      return
    }

    const profileId = targetTab.metadata.agentProfileId
    let conversationKey = targetTab.metadata.agentProfileConversationKey
    let resumeProjectId: string | undefined
    let createResumedTab = false

    // Work's permanent Workshop tab is a history/new-chat launch surface. A
    // resume must open the selected durable conversation in its own chat tab;
    // attaching it to Workshop makes old messages appear under the launcher,
    // while the next submission still follows launcher semantics and silently
    // creates a different conversation.
    if (targetTab.metadata.agentProfileBuilder === true) {
      const existing = Object.values(chatStore.chatTabs).find(tab =>
        tab.metadata?.agentProfileBuilder !== true &&
        tab.metadata?.agentProfileId === targetTab?.metadata?.agentProfileId &&
        tab.metadata?.agentProfileProjectId === targetTab?.metadata?.agentProfileProjectId &&
        tab.sessionId === session.session_id,
      )
      if (existing) {
        targetTabId = existing.tabId
        targetTab = existing
        conversationKey = existing.metadata?.agentProfileConversationKey
      } else {
        // The browser sends only the project and selected chat identities. The
        // server owns the registry key format and verifies project ownership.
        resumeProjectId = targetTab.metadata.agentProfileProjectId
          || targetTab.metadata.agentProfileConversationKey?.split(':', 1)[0]
        conversationKey = undefined
        createResumedTab = true
      }
    }

    // A Code project's history Open must not repoint the main chat at an old (for example closed) side chat: it
    // comes back as a side chat of its own, like a new tab (PLAT-827). From a side chat it still resumes in place.
    const codeProjectId = targetTab.metadata?.agentProfileProjectId
    if (profileId === 'code' && codeProjectId && conversationKey === codeProjectId && !createResumedTab) {
      const open = Object.values(chatStore.chatTabs).filter(tab => isWorkSideChatTab(tab, codeProjectId)).length
      if (open >= WORK_SIDE_CHAT_LIMIT) {
        useChatStore.getState().addToast(`This project already has ${open} side chats open. Close one first, then open this chat.`, 'error')
        return
      }
      conversationKey = workSideChatKey(codeProjectId, globalThis.crypto.randomUUID().slice(0, 8))
      createResumedTab = true
    }

    if (!conversationKey && !resumeProjectId) {
      useChatStore.getState().addToast('This chat has no product conversation binding.', 'error')
      return
    }

    // Resume is a server-owned identity change. Bind the selected historical
    // session to this tab's product conversation before the tab can submit a
    // message. That lets the shared runner find the saved native Claude/Cursor
    // session and use its real resume mechanism instead of starting empty.
    let resumedConversation
    try {
      resumedConversation = await agentApi.switchAgentProfileConversation(profileId, {
        conversation_key: conversationKey,
        resource_id: resumeProjectId,
        session_id: session.session_id,
      })
    } catch (error) {
      console.error('[ResumePreviousChat] Failed to bind product conversation', error)
      useChatStore.getState().addToast('Could not resume this chat. Its saved conversation was kept unchanged.', 'error')
      return
    }

    if (createResumedTab) {
      const builderConfig = targetTab.config
      const resumedTabId = await chatStore.createChatTab(chatHistorySessionTitle(session), {
        ...targetTab.metadata,
        agentProfileBuilder: false,
        agentProfileConversationKey: resumedConversation.conversation_key,
        agentProfileConversationId: resumedConversation.conversation_id,
      }, resumedConversation.session_id)
      if (builderConfig) chatStore.setTabConfig(resumedTabId, { ...builderConfig })
      targetTabId = resumedTabId
      targetTab = useChatStore.getState().chatTabs[resumedTabId]
    } else {
      chatStore.setTabMetadata(targetTabId, {
        agentProfileConversationKey: resumedConversation.conversation_key,
        agentProfileConversationId: resumedConversation.conversation_id,
      })
      chatStore.updateTabSessionId(targetTabId, resumedConversation.session_id)
      targetTab = useChatStore.getState().chatTabs[targetTabId]
    }

    // Clear the current conversation before resuming a different one (or the
    // same one), so the user gets a clean slate that resumes the selected chat
    // — matches New Chat's reset-in-place. On an empty landing tab this is a
    // cheap no-op (just rotates the session id).
    const events = targetTab.sessionId ? useChatStore.getState().tabEvents[targetTab.sessionId] : undefined
    const hasContent = Array.isArray(events) && events.length > 0
    if (targetTab?.sessionId !== session.session_id && hasContent) {
      chatStore.resetTabChat(targetTabId)
    }
    chatStore.updateTabSessionId(targetTabId, resumedConversation.session_id)

    const path = chatHistoryConversationPath(session)
    const title = chatHistorySessionTitle(session)
    const useTerminalRestore = chatHistoryUsesTerminalRestore(session)
    const useNativeResume = chatHistorySupportsNativeResume(session)
    const latestStore = useChatStore.getState()
    const existingContext = latestStore.getTabConfig(targetTabId)?.fileContext || []
    const nextFileContext = existingContext.filter(item => item.path !== path)

    latestStore.setTabConfig(targetTabId, {
      fileContext: nextFileContext,
      restoredConversationPath: path,
      restoredConversationSummary: undefined,
      restoredConversationTitle: title,
      restoredConversationWorkshopModeLabel: chatHistoryWorkshopModeLabel(session),
      restoredConversationRuntimeLabel: chatHistoryRuntimeLabel(session),
      restoredConversationNativeResume: useTerminalRestore || useNativeResume,
    })

    // Reattach the transport for continuation, but reopen the saved structured
    // conversation by default. Raw remains available as an explicit terminal
    // diagnostic view.
    if (useTerminalRestore || useNativeResume) {
      latestStore.setTabViewMode(targetTabId, 'tree')
      startRestoredTransportTerminal(
        resumedConversation.session_id,
        path,
        session.session_id,
        session.workspace_path || session.runtime?.workspace_path,
      )
    }
    latestStore.switchTab(targetTabId)
  }, [])
}
