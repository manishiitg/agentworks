import type { ChatTab } from '../stores/useChatStore'
import type { PollingEvent } from '../services/api-types'
import { hasWorkflowChatContent } from '../components/workflow/workflowChatTabConversion'

/**
 * The one place that decides which workflow tab a backend session lands in.
 *
 * Three callers used to answer "does this running/restored session already
 * have a tab, and if not, do I open one?" on their own: the 10s running-
 * workflow reconciler and the boot/preset-switch reconnect in
 * WorkflowLayout, and the Global Activity Monitor's open-session path in
 * workflowSessionRestore. Each keyed purely on exact backend session id --
 * and every scheduled run mints a fresh session id -- so each run opened a
 * new tab. The one caller that did try to reuse a finished lane keyed that
 * reuse on the *workflow*, not the *schedule*, so a Pulse run would take
 * over a named schedule's tab (its title flipping to "Manual Pulse") and
 * the cron fire would take it back. Keep the invariant here and nowhere
 * else; do not add a fourth caller with its own lookup.
 */

type TabMetadata = NonNullable<ChatTab['metadata']>

// Minted by SchedulerService.newScheduleSessionID (agent_go/cmd/server/
// scheduler.go): "schedule-<trigger>--<schedule id, first 8 chars>_<unixnano>".
// The trigger is "cron" or "manual"; the timestamp is what makes every run a
// new session. The middle segment is the only stable part.
const SCHEDULE_SESSION_ID = /^schedule-[^-]+--(.+)_\d+$/i

/**
 * The schedule a session belongs to, independent of which run it is or what
 * triggered it. A cron fire and a "Run now" of the same schedule share a
 * lane; two different schedules of one workflow do not; the toolbar's
 * one-off Pulse ("manual-pulse" -> "manual-p") is its own lane. Null for
 * anything that isn't a scheduler-minted session.
 */
export function scheduleLaneKey(sessionId?: string | null): string | null {
  const match = SCHEDULE_SESSION_ID.exec((sessionId || '').trim())
  return match ? match[1].toLowerCase() : null
}

/**
 * A finished tab of the *same schedule* that a newly-discovered run should
 * take over instead of opening another tab.
 *
 * The scheduler holds a per-workflow lease (`runningScheduleInSetLocked`),
 * so at most one scheduled run per workflow is live at a time -- but a
 * workflow can have several schedules plus one-off Pulse runs, and their
 * finished tabs all sit in the strip together. Only a lane that is
 * genuinely this run's own is reused:
 *  - same schedule (lane key), so Pulse never hijacks a named schedule's
 *    tab and two schedules never fight over one;
 *  - same workflow (preset), so runs never cross workflows;
 *  - still a view-only scheduled run. A tab the user promoted to an
 *    interactive Builder chat is user-owned and never recycled;
 *  - its own run is over. A streaming lane is a live run.
 */
export function reusableScheduleTabId(
  tabs: Record<string, Pick<ChatTab, 'tabId' | 'sessionId' | 'isStreaming' | 'metadata'>>,
  presetQueryId: string,
  incomingSessionId: string,
): string | null {
  const lane = scheduleLaneKey(incomingSessionId)
  if (!lane) return null
  for (const tab of Object.values(tabs)) {
    if (!tab || tab.sessionId === incomingSessionId) continue
    const meta = tab.metadata
    if (!meta || meta.mode !== 'workflow') continue
    if (!meta.isScheduledRun || !meta.isViewOnly) continue
    if (meta.userInteractiveContinuation) continue
    if (meta.presetQueryId !== presetQueryId) continue
    if (tab.isStreaming) continue
    if (scheduleLaneKey(tab.sessionId) !== lane) continue
    return tab.tabId
  }
  return null
}

type TabEvents = Record<string, PollingEvent[]>

export const WORKFLOW_BUILDER_TAB_NAME = 'Automation Builder'

/**
 * Recognize the legacy blank "Builder" tab persisted by older releases.
 * Current releases reuse it as the first persistent Chat; this predicate is
 * retained for migration and duplicate selection. Four near-copies once
 * disagreed (one keyed on a
 * different event set, one skipped the preset check, one skipped the
 * restored-conversation check), which is how a restore could land beside an
 * identical-looking empty tab instead of in it.
 *
 * createChatTab always mints a session id, so "has a sessionId" is not
 * "has content" -- content is whether that session has any real chat
 * events. The name check is what keeps a just-opened Chat tab (named from
 * its first message, no events for a moment yet) from reading as a second
 * Builder.
 */
export function isBlankWorkflowBuilderTab(
  tab: Pick<ChatTab, 'name' | 'sessionId' | 'isStreaming' | 'metadata' | 'config'>,
  presetQueryId: string,
  tabEvents: TabEvents,
): boolean {
  const meta = tab.metadata
  if (!meta || meta.mode !== 'workflow') return false
  if (meta.phaseId !== 'workflow-builder') return false
  if (meta.workshopMode === 'run') return false
  if (meta.isViewOnly === true || meta.isExecutionRun === true) return false
  if (meta.presetQueryId !== presetQueryId) return false
  if (tab.name !== WORKFLOW_BUILDER_TAB_NAME && tab.name !== 'Workflow Builder') return false
  if (tab.isStreaming) return false
  if (tab.config?.restoredConversationPath) return false
  if (!tab.sessionId) return true
  return !hasWorkflowChatContent(tabEvents[tab.sessionId])
}

/** The workflow's legacy blank Builder tab, if one is persisted. */
export function blankWorkflowBuilderTabId(
  tabs: Record<string, ChatTab>,
  presetQueryId: string,
  tabEvents: TabEvents,
): string | null {
  return Object.values(tabs)
    .filter(tab => isBlankWorkflowBuilderTab(tab, presetQueryId, tabEvents))
    .sort((a, b) => (b.lastAccessedAt ?? b.createdAt ?? 0) - (a.lastAccessedAt ?? a.createdAt ?? 0))[0]?.tabId ?? null
}

/**
 * The workflow's idle persistent Chat that an opened conversation should
 * land in. A streaming Chat is never rebound. A legacy blank Builder is a
 * valid first-time Chat and is reused rather than kept as a separate launcher.
 */
function idleWorkflowBuilderTabId(
  tabs: Record<string, ChatTab>,
  presetQueryId: string,
  tabEvents: TabEvents,
): string | null {
  return Object.values(tabs)
    .filter(tab => {
      const meta = tab.metadata
      return meta?.mode === 'workflow' &&
        meta.phaseId === 'workflow-builder' &&
        meta.isViewOnly !== true &&
        meta.isExecutionRun !== true &&
        meta.presetQueryId === presetQueryId &&
        !meta.workflowOwnerTabID &&
        !tab.isStreaming
    })
    .sort((a, b) => {
      const aBlank = isBlankWorkflowBuilderTab(a, presetQueryId, tabEvents)
      const bBlank = isBlankWorkflowBuilderTab(b, presetQueryId, tabEvents)
      if (aBlank !== bBlank) return aBlank ? 1 : -1
      return (b.lastAccessedAt ?? b.createdAt ?? 0) - (a.lastAccessedAt ?? a.createdAt ?? 0)
    })[0]?.tabId ?? null
}

/** The workflow's one persistent interactive Chat, whether idle or running. */
function persistentWorkflowBuilderTabId(
  tabs: Record<string, ChatTab>,
  presetQueryId: string,
  tabEvents: TabEvents,
): string | null {
  return Object.values(tabs)
    .filter(tab => tab.metadata?.mode === 'workflow' &&
      tab.metadata.phaseId === 'workflow-builder' &&
      tab.metadata.isViewOnly !== true &&
      tab.metadata.isExecutionRun !== true &&
      tab.metadata.presetQueryId === presetQueryId)
    .sort((a, b) => {
      const aBlank = isBlankWorkflowBuilderTab(a, presetQueryId, tabEvents)
      const bBlank = isBlankWorkflowBuilderTab(b, presetQueryId, tabEvents)
      if (aBlank !== bBlank) return aBlank ? 1 : -1
      return (b.lastAccessedAt ?? b.createdAt ?? 0) - (a.lastAccessedAt ?? a.createdAt ?? 0)
    })[0]?.tabId ?? null
}

// Keyed by presetQueryId, only for the no-sessionId case below (ensuring the
// persistent Chat exists). Two independent callers discovering "this
// workflow has no interactive tab yet" in the same window (a cold-boot race
// between the preset-restore retry and the reconnect fallback, seen live:
// two tabs, 4 seconds apart) would otherwise each pass the
// existing-tab check before either has created one. There is no `await`
// between that check and this map registration in the branch below, so the
// first caller to run always finishes registering before a second caller's
// synchronous prefix can start -- JS's run-to-completion model, not a lock
// primitive, is what makes this race-proof.
const pendingBuilderTabCreation = new Map<string, Promise<string>>()

function isBuilderSession(metadata: TabMetadata): boolean {
  return !metadata.isScheduledRun && !metadata.isBotRun && !metadata.isExecutionRun && metadata.phaseId === 'workflow-builder'
}

/** Whether an existing tab is the same *kind* of lane the session is. A
 * scheduled run and the Builder child execution it spawns can share a
 * session id; the schedule tab must not be mistaken for the chat, or vice
 * versa. */
function sameLaneKind(tab: Pick<ChatTab, 'metadata'>, metadata: TabMetadata): boolean {
  const meta = tab.metadata
  if (!meta) return false
  if (metadata.isScheduledRun) return meta.isScheduledRun === true
  if (metadata.isBotRun) return meta.isBotRun === true
  return !meta.isViewOnly
}

export type WorkflowTabResolution = {
  tabId: string
  /** existing: a tab already bound to this session. lane: took over an idle
   * tab -- the same schedule's finished tab for a scheduled run, or this
   * workflow's blank/idle Chat tab for a builder conversation. created:
   * opened a new tab. */
  via: 'existing' | 'lane' | 'created'
}

export interface ResolveWorkflowTabArgs {
  /** Read live, not a snapshot: the exact-session check must see tabs the
   * previous await created, or two discoverers of one session both open
   * a tab for it. */
  getTabs: () => Record<string, ChatTab>
  /** Needed to tell a blank Chat tab from one with a conversation in it.
   * Without it a builder session never takes over an existing tab, and the
   * no-sessionId case (below) can't tell blank from in-use at all. */
  getTabEvents?: () => TabEvents
  presetQueryId: string
  /** Omit when there is no specific session yet -- "+New chat", the
   * cold-boot/preset-switch fallback, any caller that just needs *a* blank
   * builder tab rather than a particular conversation. The store mints a
   * fresh session id; concurrent no-sessionId callers for the same workflow
   * are race-proofed (see pendingBuilderTabCreation) instead of each
   * creating their own tab. */
  sessionId?: string
  name: string
  metadata: TabMetadata
  createChatTab: (name: string, metadata: TabMetadata, sessionId?: string) => Promise<string>
  updateTabSessionId: (tabId: string, sessionId: string) => void
}

/**
 * Resolve the tab a workflow conversation belongs in -- the one place this
 * is decided, for both a known session (an existing tab bound to it, else a
 * reusable idle lane, else a new tab) and no session at all (ensure a blank
 * builder tab exists, race-proofed against concurrent callers).
 */
export async function resolveWorkflowTabForSession(args: ResolveWorkflowTabArgs): Promise<WorkflowTabResolution> {
  const tabs = args.getTabs()

  if (args.sessionId === undefined) {
    const existing = persistentWorkflowBuilderTabId(tabs, args.presetQueryId, args.getTabEvents?.() ?? {})
    if (existing) return { tabId: existing, via: 'existing' }

    const pending = pendingBuilderTabCreation.get(args.presetQueryId)
    if (pending) return { tabId: await pending, via: 'lane' }

    const creation = (async () => args.createChatTab(args.name, args.metadata))()
      .finally(() => pendingBuilderTabCreation.delete(args.presetQueryId))
    pendingBuilderTabCreation.set(args.presetQueryId, creation)
    return { tabId: await creation, via: 'created' }
  }

  const sessionId = args.sessionId
  const boundToSession = Object.values(tabs).filter(tab =>
    tab.metadata?.mode === 'workflow' && tab.sessionId === sessionId,
  )
  const existing = boundToSession.find(tab => sameLaneKind(tab, args.metadata)) ?? boundToSession[0]
  if (existing) return { tabId: existing.tabId, via: 'existing' }

  if (args.metadata.isScheduledRun) {
    const laneTabId = reusableScheduleTabId(tabs, args.presetQueryId, sessionId)
    if (laneTabId) {
      args.updateTabSessionId(laneTabId, sessionId)
      return { tabId: laneTabId, via: 'lane' }
    }
  } else if (isBuilderSession(args.metadata) && !args.metadata.workflowOwnerTabID && args.getTabEvents) {
    const chatTabId = idleWorkflowBuilderTabId(tabs, args.presetQueryId, args.getTabEvents())
    if (chatTabId) {
      args.updateTabSessionId(chatTabId, sessionId)
      return { tabId: chatTabId, via: 'lane' }
    }
  }

  const tabId = await args.createChatTab(args.name, args.metadata, sessionId)
  return { tabId, via: 'created' }
}
