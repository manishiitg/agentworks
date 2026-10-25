import type { RunningWorkflowInfo } from '../../services/api-types'
import type { ChatTab } from '../../stores/useChatStore'
import { isExternalReadOnlyWorkflowSession, isScheduledSession } from '../../utils/workflowSessionKinds'

export interface WorkflowRuntimeTabProjection {
  name: string
  metadata: NonNullable<ChatTab['metadata']>
  autoActivate: boolean
}

/** Normalize current and persisted legacy names for the interactive workflow
 * conversation. Runtime/schedule lanes keep their own explicit names.
 *
 * `isBlank` marks the workflow's fixed, never-closed "Workshop" tab (no
 * conversation in it; shows the Recent/Schedules/Bots landing view).
 * Builder-phase tabs that hold a conversation are "Chat". */
export function workflowTabDisplayName(
  tab: Pick<ChatTab, 'name' | 'metadata' | 'sessionId'>,
  isBlank?: boolean,
): string {
  if (tab.metadata?.workshopMode === 'run') {
    return 'Run chat'
  }
  // Finished schedule tabs are persisted until the user closes them and no
  // longer pass through live runtime reconciliation. Normalize an older
  // one-off Pulse lane from its durable session identity too.

  if (
    tab.metadata?.phaseId === 'workflow-builder' &&
    (tab.name === 'Automation Builder' || tab.name === 'Workflow Builder')
  ) {
    return isBlank ? 'Workshop' : 'Chat'
  }
  return tab.name
}



/**
 * A runtime-projected tab can be discovered after its session has already
 * emitted events. Formatted mode needs an explicit history catch-up before its
 * live SSE subscription can take over; terminal mode has its own retained
 * terminal restoration path.
 */
export function shouldCatchUpRunningWorkflowTranscript(
  viewMode: ChatTab['viewMode'],
  eventCount: number,
): boolean {
  return viewMode === 'formatted' && eventCount === 0
}


// Which tab a session lands in -- existing, a reclaimed lane of the same
// schedule, or new -- is decided in utils/workflowTabResolution.ts
// (resolveWorkflowTabForSession), shared by every caller that opens tabs.

/**
 * Apply a live-runtime projection without undoing an explicit user promotion.
 *
 * Runtime discovery owns whether a session is still running. It does not own
 * the presentation of a Schedule/Bot tab after the user has deliberately
 * converted that exact conversation into an interactive Builder chat. The
 * userInteractiveContinuation marker is therefore the higher-precedence fact.
 */
export function reconcileWorkflowRuntimeTab(
  tab: ChatTab,
  projection: WorkflowRuntimeTabProjection,
): ChatTab {
  if (tab.metadata?.userInteractiveContinuation === true) {
    return {
      ...tab,
      metadata: {
        ...tab.metadata,
        mode: 'workflow',
        presetQueryId: projection.metadata.presetQueryId ?? tab.metadata.presetQueryId,
        isViewOnly: false,
        isScheduledRun: false,
        scheduledJobName: undefined,
        isBotRun: false,
        botPlatform: undefined,
        userInteractiveContinuation: true,
      },
    }
  }

  // Active-session summaries can omit phase_id for a retained Builder CLI
  // turn. That summary is status information, not authority to erase the
  // interactive tab's identity. Losing `workflow-builder` here makes Cursor
  // fall back to its provider-level structured transport, so a second message
  // is queued until the current turn ends instead of being steered live.
  const preserveExistingPhase = Boolean(
    tab.metadata?.phaseId === 'workflow-builder' &&
    !projection.metadata.phaseId &&
    projection.metadata.isScheduledRun !== true,
  )
  const metadata = {
    ...tab.metadata,
    ...projection.metadata,
  }
  if (preserveExistingPhase) {
    metadata.phaseId = tab.metadata?.phaseId
    metadata.phaseName = tab.metadata?.phaseName
  }

  return {
    ...tab,
    name: preserveExistingPhase ? tab.name : projection.name,
    metadata,
  }
}

/**
 * A tab is never auto-*hidden* because its run finished or it fell out of
 * focus — visibility is unconditional.
 *
 * This was previously conditional for view-only Schedule lanes: hidden once
 * a run finished unless it was the active tab, streaming, or had running
 * background agents. That auto-hide was itself a reaction to an earlier,
 * unconditional "until the user closes it" version that let finished runs
 * pile up in the strip and reappear on every reload (tabs persist 24h with
 * isStreaming reset to false). Explicit product decision: revert to
 * user-closes-only and accept that a workflow scheduled several times a day
 * accumulates that many tabs, including across a reload — see
 * selectDurableChatState's persistence filter, which no longer excludes
 * finished Schedule tabs either.
 *
 * What now bounds the accumulation is tab *reuse*, not closing: a new run of
 * a schedule takes over that schedule's finished tab (see
 * utils/workflowTabResolution.ts), so a schedule keeps one tab however often
 * it fires. staleWorkflowTabIds below is a time-based close that is not
 * currently wired up -- see the note in WorkflowChatTabs for why.
 */
export function shouldDisplayWorkflowTab(_tab: ChatTab, _activeTabId: string | null): boolean {
  return true
}

/** How long a workflow tab may sit untouched before it is closed for you. */
export const WORKFLOW_TAB_IDLE_CLOSE_MS = 6 * 60 * 60 * 1000

/** When the user last had this tab in front of them. */
function tabIdleSince(tab: ChatTab): number {
  return tab.lastAccessedAt ?? tab.createdAt ?? 0
}

/**
 * Workflow tabs that have earned an automatic close: untouched for longer
 * than WORKFLOW_TAB_IDLE_CLOSE_MS, not the tab currently being looked at,
 * and not still running.
 *
 * Running tabs are excluded for the same reason the close button is disabled
 * on them: closing never stops the underlying run, so a tab closed mid-run
 * leaves work executing invisibly (and the still-running-workflow reconciler
 * would recreate the tab on its next poll anyway). A long-running execution
 * is closable the moment it stops.
 */
export function staleWorkflowTabIds(
  tabs: ChatTab[],
  activeTabId: string | null,
  now: number = Date.now(),
): string[] {
  return tabs
    .filter(tab => tab.metadata?.mode === 'workflow')
    .filter(tab => tab.tabId !== activeTabId)
    .filter(tab => !tab.isStreaming && !tab.hasRunningBgAgents)
    .filter(tab => now - tabIdleSince(tab) > WORKFLOW_TAB_IDLE_CLOSE_MS)
    .map(tab => tab.tabId)
}

/** Describe the top-level tab for one live workflow execution. */
export function workflowRuntimeTabProjection(
  running: RunningWorkflowInfo,
  presetQueryId: string,
): WorkflowRuntimeTabProjection | null {
  const identity = {
    sessionId: running.session_id,
    triggeredBy: running.triggered_by,
  }
  const scheduled = isScheduledSession(identity)
  const external = isExternalReadOnlyWorkflowSession(identity)

  // Discovery filters out unopened background runs before projection. Keep
  // schedule metadata here to refresh run tabs the user already opened.
  if (external && !scheduled) return null

  if (scheduled) {
    const scheduledJobName = running.title || running.preset_name || running.phase_name || 'Schedule'
    return {
      // The tab label shows the actual schedule's name (e.g. "Daily
      // execution") rather than the generic "Schedule" -- with several
      // schedules per workflow, a row of identically-labeled tabs was not
      // distinguishable at a glance. The scheduled/bot "make interactive"
      // icon (WorkflowChatTabs) still signals it's a schedule lane
      // independent of the label.
      name: scheduledJobName,
      metadata: {
        mode: 'workflow',
        presetQueryId,
        isViewOnly: true,
        isScheduledRun: true,
        scheduledJobName,
      },
      // Runtime discovery must not pull the user away from their live Chat.
      autoActivate: false,
    }
  }

  const phaseName = running.phase_name || running.title || running.preset_name || 'Running automation'
  const tabName = running.phase_id === 'workflow-builder' ? 'Automation Builder' : phaseName
  return {
    name: tabName,
    metadata: {
      mode: 'workflow',
      phaseId: running.phase_id || undefined,
      phaseName,
      presetQueryId,
    },
    autoActivate: true,
  }
}
