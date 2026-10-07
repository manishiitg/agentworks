import type { PollingEvent } from '../../services/api-types'
import type { ChatTab } from '../../stores/useChatStore'
import { isBlankWorkflowBuilderTab } from '../../utils/workflowTabResolution'

/**
 * Keep one persistent interactive Chat while retaining every opened read-only
 * run lane until the user closes it. Selecting Chat must change focus only;
 * it must not make the previously selected schedule disappear from the strip.
 */
export function selectWorkflowTabsForStrip(
  candidates: ChatTab[],
  activeTabId: string | null,
  activePresetId: string | null,
  tabEvents: Record<string, PollingEvent[]>,
): ChatTab[] {
  const interactive = candidates
    .filter(tab => tab.metadata?.isViewOnly !== true && tab.metadata?.phaseId === 'workflow-builder')
    .sort((a, b) => {
      // The active Chat always wins, blank or not. New chat leaves the active
      // tab blank on purpose; preferring a hidden non-blank duplicate here made
      // WorkflowChatTabs jump to an older conversation after each click, so a
      // fresh chat took three clicks (PLAT-665).
      if (a.tabId === activeTabId) return -1
      if (b.tabId === activeTabId) return 1
      const aBlank = isBlankWorkflowBuilderTab(a, activePresetId || '', tabEvents)
      const bBlank = isBlankWorkflowBuilderTab(b, activePresetId || '', tabEvents)
      if (aBlank !== bBlank) return aBlank ? 1 : -1
      return (b.lastAccessedAt ?? b.createdAt) - (a.lastAccessedAt ?? a.createdAt)
    })[0]
  const runTabs = candidates
    .filter(tab => tab.metadata?.isViewOnly === true)
    .sort((a, b) => a.createdAt - b.createdAt)

  return [interactive, ...runTabs]
    .filter((tab): tab is ChatTab => Boolean(tab))
    .filter((tab, index, tabs) => tabs.findIndex(item => item.tabId === tab.tabId) === index)
}
