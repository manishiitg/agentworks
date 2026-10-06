import { useEffect } from 'react'
import { useChatStore, normalizeEventViewMode, waitForChatStoreHydration, type ChatTab } from '../stores/useChatStore'
import { useWorkflowStore } from '../stores/useWorkflowStore'
import { useGlobalPresetStore } from '../stores/useGlobalPresetStore'
import { useProductSurfaceStore } from '../stores/useProductSurfaceStore'
import { activateTab } from '../utils/activateTab'
import type { ProductSurface } from '../products/productSurfaceConfig'
import type { ModeCategory } from '../stores/useModeStore'

const READ_ONLY_WORKFLOW_RESTORE_SELECTION_WINDOW_MS = 60 * 1000

export const workflowTabSortTimestamp = (tab: ChatTab) => tab.lastAccessedAt ?? tab.createdAt ?? 0

export const isInteractiveWorkflowTab = (tab: ChatTab | null | undefined): tab is ChatTab =>
  !!tab && tab.metadata?.mode === 'workflow' && tab.metadata?.isViewOnly !== true

export const isRecentExplicitReadOnlyWorkflowTab = (tab: ChatTab | null | undefined): tab is ChatTab => {
  const restoredAt = tab?.metadata?.readOnlyRestoredAt
  return !!tab &&
    tab.metadata?.mode === 'workflow' &&
    tab.metadata?.isViewOnly === true &&
    typeof restoredAt === 'number' &&
    Date.now() - restoredAt <= READ_ONLY_WORKFLOW_RESTORE_SELECTION_WINDOW_MS
}

/** Restore the selected workflow product after chat storage hydrates. */
export function useWorkflowTabRestore(
  hasCompletedInitialSetup: boolean,
  productSurface: ProductSurface,
  selectedModeCategory: ModeCategory,
) {
  const workflowPresetOwnership = useGlobalPresetStore(state =>
    state.workflowPresets.map(preset => `${preset.id}:${preset.workflowKind === 'relay'}`).join('|')
  )
  useEffect(() => {
    if (!hasCompletedInitialSetup || (productSurface !== 'agentworks' && productSurface !== 'relays') || selectedModeCategory !== 'workflow') return

    let cancelled = false

    const ensureActiveTab = async () => {
      await waitForChatStoreHydration()
      if (cancelled || useProductSurfaceStore.getState().productSurface !== productSurface) return

      // Re-enter the selected product's saved chat without opening another product.
      const chatStore = useChatStore.getState()
      const workflowStore = useWorkflowStore.getState()
      const activeTabId = chatStore.activeTabId
      const activeTab = activeTabId ? chatStore.getTab(activeTabId) : null
      const presetStore = useGlobalPresetStore.getState()
      const activePresetId = presetStore.activePresetIds.workflow
      // Goals and Relays share workflow mode and tab storage. Restoration is
      // subordinate to the product the user chose, never a new navigation intent.
      // Unknown presets wait for the manifest catalog rather than defaulting to Goals.
      const belongsToSelection = (tab: ChatTab | null | undefined) => {
        if (!tab || tab.metadata?.mode !== 'workflow') return false
        const preset = presetStore.workflowPresets.find(item => item.id === tab.metadata?.presetQueryId)
        return !!preset &&
          (preset.workflowKind === 'relay') === (productSurface === 'relays') &&
          (!activePresetId || preset.id === activePresetId)
      }

      const activeTabMatchesPreset = activeTab && belongsToSelection(activeTab) &&
        activeTab.metadata?.presetQueryId === activePresetId
      const explicitReadOnlyActiveTab = activeTabMatchesPreset && isRecentExplicitReadOnlyWorkflowTab(activeTab)
        ? activeTab
        : null
      // Tab must match workflow mode and the active preset. Read-only Schedule/Bot
      // tabs only stay active immediately after an explicit open action.
      const hasValidActiveTab = activeTabMatchesPreset &&
        (isInteractiveWorkflowTab(activeTab) || !!explicitReadOnlyActiveTab)

      // Prefer the workflow tab the user last had active for this preset.
      const workflowTabs = Object.values(chatStore.chatTabs)
        .filter(tab => belongsToSelection(tab) && isInteractiveWorkflowTab(tab) && (tab.sessionId || tab.isStreaming))
        .sort((a, b) => workflowTabSortTimestamp(b) - workflowTabSortTimestamp(a))

      const rememberedWorkflowTab = workflowStore.activeWorkflowTabId
        ? chatStore.getTab(workflowStore.activeWorkflowTabId)
        : null
      const rememberedWorkflowTabMatchesPreset = rememberedWorkflowTab &&
        belongsToSelection(rememberedWorkflowTab) &&
        isInteractiveWorkflowTab(rememberedWorkflowTab) &&
        rememberedWorkflowTab.metadata?.presetQueryId === activePresetId
      const builderTab = workflowTabs.find(tab => tab.metadata?.phaseId === 'workflow-builder')
      const streamingTab = workflowTabs.find(tab => chatStore.getTabStreamingStatus(tab.tabId) || tab.isStreaming)
      const activeWorkflowViewMode = normalizeEventViewMode(
        activeTab && belongsToSelection(activeTab)
          ? activeTab.viewMode
          : chatStore.eventViewModePreference
      )
      const targetWorkflowTab = explicitReadOnlyActiveTab || (
        activeWorkflowViewMode === 'terminal'
          ? streamingTab ||
            (hasValidActiveTab ? activeTab : null) ||
            (rememberedWorkflowTabMatchesPreset ? rememberedWorkflowTab : null) ||
            builderTab ||
            workflowTabs[0]
          : builderTab ||
            (hasValidActiveTab ? activeTab : null) ||
            (rememberedWorkflowTabMatchesPreset ? rememberedWorkflowTab : null) ||
            streamingTab ||
            workflowTabs[0]
      )

      if (targetWorkflowTab) {
        if (!hasValidActiveTab || activeTabId !== targetWorkflowTab.tabId) {
          activateTab(targetWorkflowTab.tabId)
        }

        const shouldShowWorkflowChat =
          workflowStore.showChatArea ||
          targetWorkflowTab.metadata?.phaseId === 'workflow-builder'

        if (shouldShowWorkflowChat) {
          workflowStore.setShowChatArea(true)
        }
      } else {
        // No active workflow tabs - clear activeTabId so WorkflowLayout's ChatArea
        // doesn't display content from another mode
        useChatStore.setState({ activeTabId: null })
      }
    }

    void ensureActiveTab()

    return () => {
      cancelled = true
    }
  }, [hasCompletedInitialSetup, productSurface, selectedModeCategory, workflowPresetOwnership])
}
