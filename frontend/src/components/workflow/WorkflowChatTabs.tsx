import React, { useMemo, useEffect, useCallback, useRef } from 'react'
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
import { shouldDisplayWorkflowTab, workflowTabDisplayName } from './workflowRuntimeTabProjection'
import { isBlankWorkflowBuilderTab } from '../../utils/workflowTabResolution'
import { AgentWorksChatTabItem } from '../chat/AgentWorksChatTabItem'
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
    closeTab,
  } = useChatStore(useShallow(state => ({
    chatTabs: state.chatTabs,
    activeTabId: state.activeTabId,
    tabEvents: state.tabEvents,
    closeTab: state.closeTab,
  })))

  const setShowChatArea = useWorkflowStore(state => state.setShowChatArea)
  const setFocusedPane = useWorkflowStore(state => state.setFocusedPane)
  const activePresetId = useGlobalPresetStore(state => state.activePresetIds.workflow)
  const workflowPresets = useGlobalPresetStore(state => state.workflowPresets)
  const isRelaySurface = useProductSurfaceStore(state => state.productSurface === 'relays')

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

  // Filter to workflow tabs for the active preset, but always keep the active
  // workflow tab visible. Scheduled-run restores can briefly lack a preset match
  // while the tab is being created/switched, and hiding the active tab makes the
  // restore look like it failed.
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
    // AgentWorks owns one persistent interactive Chat. Old duplicate tabs may
    // remain in persisted browser state, but they are no longer presented as
    // separate conversations. Opened read-only run lanes remain beside it
    // until explicitly closed; selecting Chat changes focus, not visibility.
    return selectWorkflowTabsForStrip(visible, activeTabId, activePresetId, tabEvents)
  }, [chatTabs, activePresetId, activeTabId, tabEvents])

  // Migrate a browser that persisted the old Workshop + Chat pair: if its
  // hidden blank Workshop tab is still active, move focus to the retained
  // conversation so the visible Chat and the rendered transcript agree.
  useEffect(() => {
    const persistentChat = activeWorkflowTabs.find(tab =>
      tab.metadata?.phaseId === 'workflow-builder' && tab.metadata?.isViewOnly !== true,
    )
    const current = activeTabId ? chatTabs[activeTabId] : undefined
    const currentIsInteractiveDuplicate = current?.metadata?.mode === 'workflow' &&
      current.metadata.presetQueryId === activePresetId &&
      current.metadata.phaseId === 'workflow-builder' &&
      current.metadata.isViewOnly !== true
    if (persistentChat && currentIsInteractiveDuplicate && persistentChat.tabId !== activeTabId) {
      activateTab(persistentChat.tabId)
    }
  }, [activePresetId, activeTabId, activeWorkflowTabs, chatTabs])

  // Skip auto-close on initial mount
  const hasRenderedRef = useRef(false)

  const handleTabClick = useCallback((tabId: string) => {
    activateTab(tabId)
    onSelectChat?.()
    // On narrow screens the toolbar stays visible while only one content pane
    // is shown. A chat-tab selection must therefore bring the chat pane back.
    if (window.innerWidth < 768) setFocusedPane('chat')
  }, [onSelectChat, setFocusedPane])

  const handleCloseTab = useCallback((tabId: string) => {
    const nextWorkflowTabId = activeTabId === tabId
      ? activeWorkflowTabs.find(tab => tab.tabId !== tabId)?.tabId ?? null
      : null

    void closeTab(tabId, false).then(() => {
      if (nextWorkflowTabId) {
        activateTab(nextWorkflowTabId)
      }
    })
  }, [activeTabId, activeWorkflowTabs, closeTab])

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

  return (
    <>
    {/* min-w-0 without flex-1: this strip takes only the width its tab pills
        need, so the toolbar's other controls sit immediately after it. */}
    <div className={embedded
      ? 'flex min-w-0 shrink-0'
      : 'shrink-0 border-b border-gray-200 bg-gray-50 dark:border-gray-700 dark:bg-gray-800'}>
      <div className={embedded
        ? 'flex min-w-0 items-center gap-1'
        : 'flex min-w-0 items-center gap-1 px-2 py-1'}>
        <div className="flex min-w-0 items-center gap-1 overflow-x-auto">
          {activeWorkflowTabs.map((tab) => {
            const isBlank = isBlankWorkflowBuilderTab(tab, activePresetId || '', tabEvents)
            const isPersistentChat = tab.metadata?.isViewOnly !== true && tab.metadata?.phaseId === 'workflow-builder'
            const workflowName = workflowPresets.find(preset => preset.id === tab.metadata?.presetQueryId)?.label?.trim()
              || (isRelaySurface ? 'Relay' : 'Workflow')
            return (
              <AgentWorksChatTabItem
                key={tab.tabId}
                tab={tab}
                isActive={tab.tabId === activeTabId}
                // The persistent Chat is never closeable.
                canClose={!isPersistentChat && activeWorkflowTabs.length > 1}
                isBlank={false}
                displayName={isPersistentChat ? workflowName : workflowTabDisplayName(tab, isBlank)}
                titleOverride={isPersistentChat ? workflowName : undefined}
                onTabClick={handleTabClick}
                onCloseTab={handleCloseTab}
                onMakeInteractive={handleMakeInteractive}
              />
            )
          })}
        </div>
      </div>
    </div>
    </>
  )
}
