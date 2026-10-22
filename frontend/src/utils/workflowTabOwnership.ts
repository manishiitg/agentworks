import type { ChatTab } from '../stores/useChatStore'

/**
 * A workflow surface may only render a tab owned by the selected workflow.
 *
 * Old persisted builder tabs can predate presetQueryId. Keep that narrow
 * compatibility path only when there is no explicitly-owned tab for the active
 * workflow. A tab explicitly owned by another workflow must never be accepted.
 */
export function workflowTabBelongsToPreset(
  tab: ChatTab | null | undefined,
  activePresetId: string | null,
  tabs: Record<string, ChatTab>,
): boolean {
  if (!tab || tab.metadata?.mode !== 'workflow') return false

  const tabPresetId = tab.metadata?.presetQueryId
  if (tabPresetId) return tabPresetId === activePresetId

  if (!activePresetId || tab.metadata?.phaseId !== 'workflow-builder') return false

  const hasExplicitTabForPreset = Object.values(tabs).some(candidate =>
    candidate.metadata?.mode === 'workflow' &&
    candidate.metadata?.presetQueryId === activePresetId &&
    (candidate.sessionId || candidate.isStreaming)
  )
  return !hasExplicitTabForPreset
}

export function activeWorkflowTabIdForPreset(
  activeTabId: string | null,
  activePresetId: string | null,
  tabs: Record<string, ChatTab>,
): string | undefined {
  const tab = activeTabId ? tabs[activeTabId] : undefined
  return workflowTabBelongsToPreset(tab, activePresetId, tabs) ? activeTabId ?? undefined : undefined
}

type TabEventCounts = Record<string, ReadonlyArray<unknown> | undefined>

/**
 * The selected workflow's active tab already holds a last-known transcript in
 * memory, so the pane can show it at once while the reconnect refreshes it.
 */
export function activeWorkflowTabHasCachedConversation(
  activeTabId: string | null,
  activePresetId: string | null,
  tabs: Record<string, ChatTab>,
  tabEvents: TabEventCounts,
): boolean {
  const tabId = activeWorkflowTabIdForPreset(activeTabId, activePresetId, tabs)
  const sessionId = tabId ? tabs[tabId]?.sessionId : undefined
  return !!sessionId && (tabEvents[sessionId]?.length ?? 0) > 0
}

/**
 * The workflow's persistent Chat when its transcript is still in memory: the
 * same tab resolveWorkflowTabForSession's no-session case settles on, picked
 * the same way (most recently opened). Switching back to a workflow opens it
 * immediately; the full session resolution then runs in the background and
 * moves on only when it finds a different conversation.
 */
export function cachedWorkflowTabIdForPreset(
  presetId: string,
  tabs: Record<string, ChatTab>,
  tabEvents: TabEventCounts,
): string | undefined {
  let best: ChatTab | undefined
  for (const tab of Object.values(tabs)) {
    const meta = tab.metadata
    if (meta?.mode !== 'workflow' || meta.presetQueryId !== presetId) continue
    if (meta.phaseId !== 'workflow-builder' || meta.isViewOnly === true || meta.isExecutionRun === true) continue
    if (!tab.sessionId || (tabEvents[tab.sessionId]?.length ?? 0) === 0) continue
    if (!best || (tab.lastAccessedAt ?? tab.createdAt ?? 0) > (best.lastAccessedAt ?? best.createdAt ?? 0)) best = tab
  }
  return best?.tabId
}

/** A server-revalidated return is an explicit selection, not an old browser tab. */
export function isRecentWorkspaceReturnTab(tab: ChatTab | null | undefined, presetId: string | null, now = Date.now()): tab is ChatTab {
  const returned = tab?.metadata?.workspaceReturnAt
  return Boolean(tab?.sessionId && tab.metadata?.presetQueryId === presetId && typeof returned === 'number' && returned <= now && now - returned < 60000)
}
