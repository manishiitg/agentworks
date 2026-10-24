import { closeWorkspaceChatTab } from '../../utils/closeWorkspaceChatTab'
import { agentApi } from '../../services/api'
import { useWorkflowBuilderBinding, publishWorkflowBuilderBinding } from '../../hooks/useWorkflowBuilderBindings'
import { hydrateWorkflowOwnerTabs } from '../../utils/ensureWorkflowBuilderTab'
import { captureChatIdentity, isChatIdentityCurrent } from '../../utils/chatIdentity'
import React, { useMemo, useEffect, useCallback, useRef, useState } from 'react'
import { useShallow } from 'zustand/react/shallow'
import { useChatStore, type ChatTab } from '../../stores/useChatStore'
import { activateTab } from '../../utils/activateTab'
import { useWorkflowStore } from '../../stores/useWorkflowStore'
import { useGlobalPresetStore } from '../../stores/useGlobalPresetStore'
import { useProductSurfaceStore } from '../../stores/useProductSurfaceStore'
import {
  convertObservedWorkflowTabToInteractive,
  isMisclassifiedRestoredWorkflowChat,
} from './workflowChatTabConversion'
import { shouldDisplayWorkflowTab } from './workflowRuntimeTabProjection'
import { ProjectAgentTabs } from '../chat/ProjectAgentTabs'
import { StepInspectionTabItems } from '../chat/StepInspectionTabItems'
import { selectStepInspection, stepInspectionState } from '../../utils/stepInspection'
import { selectWorkflowTabsForStrip } from './workflowTabStripSelection'

// ---------------------------------------------------------------------------
// WorkflowChatTabs — parent component
// ---------------------------------------------------------------------------

/**
 * Mini ChatTabs component for workflow mode chat area
 * Only shows workflow tabs that are active (have sessionId or isStreaming)
 */
interface WorkflowChatTabsProps {
  // When true, render inline (no bordered/background bar wrapper) so the strip can
  // be embedded inside the WorkflowToolbar row instead of being its own bar.
  embedded?: boolean
  onSelectChat?: () => void
}

export const WorkflowChatTabs: React.FC<WorkflowChatTabsProps> = ({ embedded = false, onSelectChat }) => {
  const {
    chatTabs,
    activeTabId,
    tabEvents,
  } = useChatStore(useShallow(state => ({
    chatTabs: state.chatTabs,
    activeTabId: state.activeTabId,
    tabEvents: state.tabEvents,
  })))

  const setShowChatArea = useWorkflowStore(state => state.setShowChatArea)
  const setFocusedPane = useWorkflowStore(state => state.setFocusedPane)
  const activePresetId = useGlobalPresetStore(state => state.activePresetIds.workflow)
  const workflowPresets = useGlobalPresetStore(state => state.workflowPresets)
  const isRelaySurface = useProductSurfaceStore(state => state.productSurface === 'relays')

  const [opening, setOpening] = useState(false)
  const binding = useWorkflowBuilderBinding(activePresetId)
  useEffect(() => {
    if (binding) void hydrateWorkflowOwnerTabs(binding).catch(() => {})
  }, [binding])
  const mutateTab = useCallback(async (action: string, chatId?: string, name?: string, options?: { pinned?: boolean; chat_ids?: string[] }) => {
    const preset = workflowPresets.find(item => item.id === activePresetId)
    if (!preset?.selectedFolder?.filepath) return
    const identity = captureChatIdentity()
    const changed = await agentApi.manageWorkflowChats(preset.id, preset.selectedFolder.filepath, action, chatId, name, options)
    const response = await agentApi.getWorkflowBuilderSessions([{ preset_query_id: preset.id, workspace_path: preset.selectedFolder.filepath }])
    if (!isChatIdentityCurrent(identity)) return
    const next = response.bindings[preset.id]
    if (next) {
      publishWorkflowBuilderBinding(next, identity, true)
      await hydrateWorkflowOwnerTabs(next)
    }
    if (action === 'open' && changed.chat) {
      const tab = Object.values(useChatStore.getState().chatTabs).find(tab => tab.sessionId === changed.chat?.session_id)
      if (tab) { activateTab(tab.tabId); setShowChatArea(true); onSelectChat?.() }
    }
  }, [activePresetId, workflowPresets, setShowChatArea, onSelectChat])

  // Repair tabs already corrupted by the old Restore path. The durable
  // restoredConversationPath marker proves this is an interactive restore,
  // while isViewOnly proves it incorrectly retained Schedule/full-run state.
  useEffect(() => {
    const affected = Object.values(chatTabs).filter(isMisclassifiedRestoredWorkflowChat)
    if (affected.length === 0) return
    useChatStore.setState(state => {
      const nextTabs = { ...state.chatTabs }
      affected.forEach(tab => {
        const current = nextTabs[tab.tabId]
        if (current && isMisclassifiedRestoredWorkflowChat(current)) {
          nextTabs[tab.tabId] = convertObservedWorkflowTabToInteractive(current)
        }
      })
      return { chatTabs: nextTabs }
    })
  }, [chatTabs])

  // Scheduled-run restores can briefly lack a preset binding. Include that
  // active tab as a candidate; the strip rejects bindings to another workflow.
  const activeWorkflowTabs = useMemo(() => {
    const allTabs = Object.values(chatTabs)
    const matched = allTabs.filter(tab =>
      tab.metadata?.mode === 'workflow' &&
      tab.metadata.presetQueryId === activePresetId &&
      shouldDisplayWorkflowTab(tab, activeTabId)
    )
    const activeTab = activeTabId ? chatTabs[activeTabId] : undefined
    const activeWorkflowTab = activeTab?.metadata?.mode === 'workflow' ? activeTab : undefined

    const visibleById = new Map<string, ChatTab>()
    matched.forEach(tab => visibleById.set(tab.tabId, tab))
    if (activeWorkflowTab) {
      visibleById.set(activeWorkflowTab.tabId, activeWorkflowTab)
    }

    const visible = visibleById.size > 0
      ? Array.from(visibleById.values())
      : allTabs.filter(tab =>
          tab.metadata?.mode === 'workflow' &&
          tab.metadata?.phaseId === 'workflow-builder' &&
          !tab.metadata?.presetQueryId
        )
    // Registered named tabs and execution inspection lanes keep their own identities.
    return selectWorkflowTabsForStrip(visible, activeTabId, activePresetId, tabEvents)
  }, [chatTabs, activePresetId, activeTabId, tabEvents])

  // The strip always keeps the active Chat (selectWorkflowTabsForStrip), so
  // focus is never moved to a hidden duplicate here. An effect that did that
  // sent New chat back to an older conversation (PLAT-665).

  // Skip auto-close on initial mount
  const hasRenderedRef = useRef(false)

  const showTab = useCallback((tabId: string) => {
    activateTab(tabId)
    onSelectChat?.()
    // On narrow screens the toolbar stays visible while only one content pane
    // is shown. A chat-tab selection must therefore bring the chat pane back.
    if (window.innerWidth < 768) setFocusedPane('chat')
  }, [onSelectChat, setFocusedPane])

  const handleTabClick = useCallback((tabId: string) => {
    const tab = useChatStore.getState().getTab(tabId)
    if (tab) selectStepInspection(tabId, tab.sessionId, null)
    showTab(tabId)
  }, [showTab])

  const handleCloseTab = useCallback((tabId: string) => {
    const tab = useChatStore.getState().getTab(tabId)
    const close = tab?.metadata?.workflowOwnerTabID ? mutateTab('close', tab.metadata.workflowOwnerTabID) : closeWorkspaceChatTab(tabId)
    void close.catch(() => {
      useChatStore.getState().addToast('Could not close this chat. Try again.', 'error')
    })
  }, [mutateTab])

  // Make the scheduled/bot conversation interactive without changing its
  // identity. The backend routes the next message to this session's retained
  // live tmux first; if the pane is gone, it resumes the same native coding-CLI
  // conversation. Rotating the session ID here would incorrectly fork away
  // from both the tmux and its conversation context.
  const handleMakeInteractive = useCallback((tabId: string) => {
    const chatStore = useChatStore.getState()
    const tab = chatStore.getTab(tabId)
    if (!tab) return

    useChatStore.setState((state) => {
      const current = state.chatTabs[tabId]
      if (!current) return state
      return {
        chatTabs: {
          ...state.chatTabs,
          [tabId]: convertObservedWorkflowTabToInteractive(current),
        },
      }
    })
    activateTab(tabId)
    setShowChatArea(true)
    setFocusedPane('chat')
  }, [setFocusedPane, setShowChatArea])

  // DISABLED 2026-09-04 (shipped and reverted same day): staleWorkflowTabIds
  // treats a tab as "safe to sweep" using isStreaming/hasRunningBgAgents,
  // but reconcileRunningWorkflowTab (WorkflowLayout.tsx) treats a session as
  // still "running" on a broader definition that also covers idle/waiting --
  // which is the normal resting state of an Automation Builder chat between
  // messages. So the sweep was closing perfectly ordinary idle tabs, and the
  // reconciler's next ~10s poll recreated them from the still-tracked
  // backend session -- unprompted close/reopen flicker, live in production.
  // Re-enable only once staleWorkflowTabIds (or its caller) checks the same
  // "is this session still running" signal reconcileRunningWorkflowTab does,
  // not the narrower streaming/bg-agent flags. staleWorkflowTabIds and its
  // tests are left in place -- the function itself is correct, it just
  // needs a correct "is this session done" input.

  // Close chat area when all workflow tabs are closed (but not on first render)
  useEffect(() => {
    if (!hasRenderedRef.current) {
      hasRenderedRef.current = true
      return
    }
    if (activeWorkflowTabs.length === 0) {
      setShowChatArea(false)
    }
  }, [activeWorkflowTabs.length, setShowChatArea])

  // Don't render if no active workflow tabs
  if (activeWorkflowTabs.length === 0) {
    return null
  }

  const ordered = binding?.tabs
    ? [...binding.tabs.flatMap(chat => activeWorkflowTabs.filter(tab => tab.sessionId === chat.session_id)),
      ...activeWorkflowTabs.filter(tab => !tab.metadata?.workflowOwnerTabID)]
    : activeWorkflowTabs
  const resolvedTabs = ordered.length ? ordered : activeWorkflowTabs
  const main = resolvedTabs.find(tab => tab.metadata?.workflowOwnerTabIsMain && tab.metadata.agentProfileTabCanWrite)
    || resolvedTabs.find(tab => !tab.metadata?.isViewOnly && tab.metadata?.phaseId === 'workflow-builder') || resolvedTabs[0]
  const tabs = [main, ...resolvedTabs.filter(tab => tab.tabId !== main.tabId)]
  const listed = (binding?.tabs || []).filter(chat => chat.id !== main.metadata?.workflowOwnerTabID).map(chat => ({
    conversation_key: chat.id, conversation_id: chat.id, session_id: chat.session_id, name: chat.name,
    pinned: chat.pinned !== false, position: chat.position || 0, can_write: chat.can_write,
    tab_owner_id: chat.owner_id, tab_owner_name: chat.owner_name,
  }))
  return <div className={embedded ? 'flex min-w-0 flex-1' : 'shrink-0 border-b border-border bg-background px-2 py-1'}>
    <ProjectAgentTabs tabs={tabs} listed={listed} canonicalTabId={main.tabId}
      projectName={workflowPresets.find(preset => preset.id === activePresetId)?.label?.trim() || (isRelaySurface ? 'Relay' : 'Goal')}
      surface={isRelaySurface ? 'relays' : 'agentworks'} allowSideChats={!!binding?.tabs} opening={opening}
      onOpen={async name => { setOpening(true); try { await mutateTab('open', undefined, name) } finally { setOpening(false) } }}
      onRename={async (tab, name) => { await mutateTab('rename', tab.metadata?.workflowOwnerTabID, name) }}
      onOrder={async chat_ids => { await mutateTab('order', undefined, undefined, { chat_ids }) }}
      onPin={async (chatId, pinned) => { await mutateTab('pin', chatId, undefined, { pinned }) }}
      onSelectTab={handleTabClick} onCloseTab={handleCloseTab} onMakeInteractive={handleMakeInteractive}
      isTabActive={tab => tab.tabId === activeTabId && !stepInspectionState(tab).selected}
      renderAfterTab={tab => <StepInspectionTabItems tab={tab} active={tab.tabId === activeTabId} onActivate={showTab} />} />
  </div>
}
