import { useLLMStore } from '../stores/useLLMStore'
import { useAppStore } from '../stores/useAppStore'
import { useChatStore } from '../stores/useChatStore'
import { useModeStore } from '../stores/useModeStore'
import { useWorkflowStore } from '../stores/useWorkflowStore'
import { activateWorkflowTab } from './workflowNavigation'

/**
 * The single sanctioned way to bring an existing tab on-screen.
 *
 * "Show this tab" is three base writes across three stores:
 *   - useAppStore.showWorkflowsOverview  (the overlay that covers everything)
 *   - useModeStore.selectedModeCategory  (workflow pane vs chat pane)
 *   - useChatStore.activeTabId           (which session shows)
 * plus, for workflow tabs, useGlobalPresetStore.activePresetIds.workflow
 * (which automation owns the report/workspace pane).
 *
 * Every navigation entry point used to re-implement that sequence by hand, and
 * missing any one step left the selected tab hidden (e.g. behind the Workflows
 * Overview, or in the wrong pane). Routing all of them through activateTab makes
 * it impossible to set those three inconsistently: the tab's own metadata is the
 * single source of truth for which mode/pane it belongs to.
 *
 * This is the low-level primitive for navigation only. It does NOT create,
 * restore, or fetch — callers create/restore the tab first (which yields a
 * tabId) and then call activateTab to reveal it. For pure bookkeeping where the
 * visible surface must not change (auto-select after close, internal shuffling),
 * call useChatStore.switchTab directly instead.
 *
 * @returns true if the tab existed and was activated, false otherwise.
 */
export function activateTab(tabId: string): boolean {
  const chat = useChatStore.getState()
  const tab = chat.chatTabs[tabId]
  if (!tab) return false

  // Clearing the overlay is required for the pane to actually show — switching
  // the tab/mode alone is not enough.
  useLLMStore.getState().setShowLLMModal(false)
  useAppStore.getState().setAdminPage(null)
  useAppStore.getState().setShowWorkflowsOverview(false)
  useAppStore.getState().setShowSchedulesOverview(false)

  // The tab metadata is the source of truth for which pane it belongs to.
  const mode = tab.metadata?.mode ?? 'multi-agent'
  if (mode === 'workflow' && tab.metadata?.presetQueryId) {
    return activateWorkflowTab(tabId)
  }

  if (useModeStore.getState().selectedModeCategory !== mode) {
    useModeStore.getState().setModeCategory(mode)
  }
  if (mode === 'workflow') {
    // Legacy workflow tabs without ownership metadata cannot update the report
    // selection, but remain renderable during the migration window.
    useWorkflowStore.getState().setShowChatArea(true)
  }

  chat.switchTab(tabId)
  return true
}
