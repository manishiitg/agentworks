import { captureChatIdentity, assertChatIdentityCurrent } from './chatIdentity'
import { useChatStore } from '../stores/useChatStore'
import { useModeStore } from '../stores/useModeStore'
import { agentApi } from '../services/api'
import type { PollingEvent } from '../services/api-types'
import { truncateTabTitle } from './textUtils'
import { applyLiveInputConfirmations, resolveLiveInputConfirmations, splitLiveInputConfirmations } from './liveInputReceipt'
import { keepUnechoedProvisionals, pendingQueuedProvisionals } from './clientMessageIdentity'
import type { LiveInputConfirmationUpdate } from './liveInputReceipt'
import axios from 'axios'

const TAG = '[SessionRestore]'

type RuntimeSessionState = {
  status: string
  hasRunningBackgroundAgents?: boolean
  isSyntheticTurn?: boolean
  canSteer?: boolean
  restoredEvents?: PollingEvent[]
}

function isForegroundStreaming(state: RuntimeSessionState): boolean {
  if (state.status !== 'running') return false
  // Background-only work should not lock the composer after restore.
  // Synthetic auto-notification turns are activity, but they should not queue user input.
  return !state.isSyntheticTurn && (!state.hasRunningBackgroundAgents || !!state.canSteer)
}

/**
 * Per-session async lock to prevent duplicate restores.
 * If restoreSession is called concurrently for the same session,
 * subsequent calls return the existing Promise.
 */
const restoreInProgress = new Map<string, Promise<string>>()
const hydrateRequestVersions = new Map<string, number>()

/**
 * Apply session status (completed/streaming/restored) to a tab.
 */
function applySessionStatus(tabId: string, state: RuntimeSessionState): void {
  const chatStore = useChatStore.getState()
  const isDone = state.status === 'completed' || state.status === 'stopped'
  const isError = state.status === 'error'
  chatStore.setTabCompleted(tabId, isDone)
  chatStore.setTabStreaming(tabId, isDone || isError ? false : isForegroundStreaming(state))
  chatStore.setTabHasRunningBgAgents(tabId, !!state.hasRunningBackgroundAgents)
  chatStore.setTabSyntheticTurn(tabId, !!state.isSyntheticTurn)
  chatStore.setTabCanSteer(tabId, !!state.canSteer)
  if (isDone || isError) {
    chatStore.setTabMetadata(tabId, { isRestored: true })
  }
}

/**
 * Unified session restoration function.
 * Handles all restore flows: auto-restore, page-refresh hydration, and inline history selection.
 *
 * Returns the tabId for the restored session.
 */
export async function restoreSession(
  sessionId: string,
  options?: {
    title?: string
    source?: string
    skipConfigRestore?: boolean
    workspacePath?: string
  }
): Promise<string> {
  // Async lock: if already restoring this session, return the existing promise
  const restoreKey = `${captureChatIdentity()}:${sessionId}`
  const existing = restoreInProgress.get(restoreKey)
  if (existing) {
    console.log(`${TAG} Dedup hit for ${sessionId} (source=${options?.source}), returning existing promise`)
    return existing
  }

  const promise = doRestoreSession(sessionId, options)
  restoreInProgress.set(restoreKey, promise)

  try {
    return await promise
  } finally {
    restoreInProgress.delete(restoreKey)
  }
}

async function doRestoreSession(
  sessionId: string,
  options?: {
    title?: string
    source?: string
    skipConfigRestore?: boolean
    workspacePath?: string
  }
): Promise<string> {
  const identity = captureChatIdentity()
  const src = options?.source || 'unknown'
  console.log(`${TAG} Start session=${sessionId} source=${src} title=${options?.title ?? '(none)'}`)
  const chatStore = useChatStore.getState()

  // Step 1: Check for existing tab with events already loaded
  const existingTabWithSession = Object.values(chatStore.chatTabs).find(tab => tab.sessionId === sessionId)
  const existingTab = existingTabWithSession
  const existingEventCount = existingTab ? chatStore.getTabEvents(sessionId).length : 0
  if (existingTab) {
    if (existingEventCount > 0) {
      console.log(`${TAG} [${src}] Tab ${existingTab.tabId} already has ${existingEventCount} events, refreshing runtime state`)
    } else {
      console.log(`${TAG} [${src}] Tab ${existingTab.tabId} exists but has 0 events, will hydrate`)
    }
  }

  // Chat sessions are in-memory on the backend now — there is no persisted
  // session metadata to fetch. Tab state (title, config) is the frontend's
  // responsibility; session status comes from the polling API.
  const tabMode = 'multi-agent' as const
  useModeStore.getState().setModeCategory('multi-agent')

  let tabId: string
  if (existingTab) {
    tabId = existingTab.tabId
    console.log(`${TAG} [${src}] Reusing existing tab ${tabId}`)
  } else {
    const title = truncateTabTitle(options?.title || 'Chat')
    tabId = await chatStore.createChatTab(
      title,
      { mode: tabMode, isRestored: false },
      sessionId,
    )
    assertChatIdentityCurrent(identity)
    console.log(`${TAG} [${src}] Created tab ${tabId} mode=${tabMode}`)
  }

  // Step 7: Sync runtime state / events
  try {
    if (existingEventCount > 0) {
      const currentLastIndex = chatStore.getTabLastEventIndex(sessionId)
      const runtime = await agentApi.getSessionEvents(sessionId, currentLastIndex, {
        durableChat: true,
        workspacePath: options?.workspacePath || existingTab?.metadata?.agentProfileWorkspace,
      })
      assertChatIdentityCurrent(identity)
      applySessionStatus(tabId, {
        status: runtime.session_status,
        hasRunningBackgroundAgents: runtime.has_running_background_agents,
        isSyntheticTurn: runtime.is_synthetic_turn,
        canSteer: runtime.can_steer,
      })
      if (runtime.events.length > 0) {
        appendRestoredLiveTail(sessionId, runtime.events)
      }
      const cursor = runtime.latest_sequence ?? runtime.last_processed_index
      // A forward `since` read: has_more means "newer rows", so it must not
      // touch the older-history pager established by the initial restore.
      if (cursor !== undefined) {
        chatStore.setTabLastEventIndex(sessionId, cursor)
      }
      console.log(`${TAG} [${src}] Refreshed runtime state for existing tab ${tabId}`)
    } else {
      const runtime = await hydrateTabEvents(sessionId, {
        workspacePath: options?.workspacePath || existingTab?.metadata?.agentProfileWorkspace,
        fallbackToChatHistory: true,
      })
      assertChatIdentityCurrent(identity)
      applySessionStatus(tabId, runtime)
      const eventCount = chatStore.getTabEvents(sessionId).length
      console.log(`${TAG} [${src}] Hydrated ${eventCount} events`)
    }
  } catch (err) {
    assertChatIdentityCurrent(identity)
    if (isNotFoundError(err) && existingEventCount > 0) {
      console.log(`${TAG} [${src}] Session ${sessionId} was not found; keeping locally restored events`)
      applySessionStatus(tabId, {
        status: 'completed',
        hasRunningBackgroundAgents: false,
        isSyntheticTurn: false,
        canSteer: false,
      })
    } else {
      console.error(`${TAG} [${src}] Failed to sync runtime state for ${sessionId}:`, err)
    }
  }

  console.log(`${TAG} [${src}] Done session=${sessionId} tab=${tabId}`)
  return tabId
}

function isNotFoundError(error: unknown): boolean {
  return axios.isAxiosError(error) && error.response?.status === 404
}


// appendTimelineAndApplyConfirmations lands a timeline window and consumes
// its durability receipts in one ordering: rows first, verdicts second,
// against the merged store. The synchronous append keeps row visibility
// immediate so a receipt in this same window matches a row it arrived
// with — the live path and restore share this helper rather than
// maintaining two ingestion orderings. Verdicts apply via patchTabEvents
// so micro-batched events from the same window still flush afterwards.
export function appendTimelineAndApplyConfirmations(sessionId: string, timelineEvents: PollingEvent[], confirmations: LiveInputConfirmationUpdate[]) {
  const chatStore = useChatStore.getState()
  if (timelineEvents.length > 0) chatStore._addTabEventsImmediate(sessionId, timelineEvents)
  if (confirmations.length === 0) return
  // patchTabEvents, not get+set: setTabEvents clears the micro-batch
  // buffer, dropping tool events that arrived in this same window.
  chatStore.patchTabEvents(sessionId, events => applyLiveInputConfirmations(events, confirmations))
}

// appendRestoredLiveTail appends a raw live-tail window the same way the live
// path ingests it: durability receipts never enter the timeline and upgrade
// the merged rows they name. A receipt-only window appends nothing. Without
// receipts the call stays on the micro-batched path exactly as before.
export function appendRestoredLiveTail(sessionId: string, incoming: ReadonlyArray<PollingEvent>) {
  const chatStore = useChatStore.getState()
  const { timelineEvents, confirmations } = splitLiveInputConfirmations(incoming)
  if (confirmations.length === 0) {
    if (timelineEvents.length > 0) chatStore.addTabEvents(sessionId, timelineEvents)
    return
  }
  if (timelineEvents.length > 0) chatStore._addTabEventsImmediate(sessionId, timelineEvents)
  // Rows carry their own identity (client_message_id / message_id), so the
  // shared append+apply consumes this window's receipts directly.
  appendTimelineAndApplyConfirmations(sessionId, [], confirmations)
}

/**
 * An interactive chat tab: not a read-only, scheduled/bot-observed or
 * execution tab. Only these restore through the compact view.
 */
export function isInteractiveChatSession(sessionId: string): boolean {
  const tab = Object.values(useChatStore.getState().chatTabs ?? {}).find(candidate => candidate.sessionId === sessionId)
  const metadata = tab?.metadata
  return Boolean(tab) && !metadata?.isViewOnly && !metadata?.isExecutionRun && !metadata?.isBotRun
}

/** Load the canonical, bounded chat page from SQLite. */
export async function hydrateTabEvents(
  sessionId: string,
  options: {
    workspacePath?: string
    fallbackToChatHistory?: boolean
    // Kept for callers that explicitly request history. Durable history is
    // now the default for every session, so this no longer changes behavior.
    preferChatHistory?: boolean
    // Restore the bounded, formatted UI trace for every retained chat. It
    // supplies tool calls that structured provider history represents only as
    // function markers; raw terminal frames are never requested.
    includeUiEvents?: boolean
    // Compact restore: the latest turn complete, older turns as messages only.
    // Defaults to on for an interactive chat tab and off for everything else
    // (read-only / scheduled / bot / execution tabs, terminal views, which
    // keep the full durable page). Pass false to force the full page.
    compact?: boolean
  } = {},
): Promise<RuntimeSessionState> {
  const identity = captureChatIdentity()
  const chatStore = useChatStore.getState()
  const compact = options.compact ?? isInteractiveChatSession(sessionId)
  const eventsAtStart = chatStore.getTabEvents(sessionId)
  const startingIDs = new Set(eventsAtStart.map(event => event.id).filter(Boolean))
  const requestKey = `${identity}:${sessionId}`
  const requestVersion = (hydrateRequestVersions.get(requestKey) || 0) + 1
  hydrateRequestVersions.set(requestKey, requestVersion)
  let response: Awaited<ReturnType<typeof agentApi.getRecentChatEvents>>
  try {
    response = await (compact
      ? agentApi.getRecentChatEvents(sessionId, options.workspacePath, true)
      : agentApi.getRecentChatEvents(sessionId, options.workspacePath))
  } catch (error) {
    if (!isNotFoundError(error)) throw error
    // A chat that never received a message has no journal rows. That is an
    // empty conversation, not a failed restore: surfacing it as an error kept
    // callers on "Loading conversation…" until their give-up timers fired.
    assertChatIdentityCurrent(identity)
    chatStore.setTabHasMoreOlderEvents(sessionId, false)
    chatStore.setTabHistoryPagination(sessionId, null)
    return {
      status: 'inactive',
      hasRunningBackgroundAgents: false,
      isSyntheticTurn: false,
      canSteer: false,
      restoredEvents: chatStore.getTabEvents(sessionId),
    }
  }
  assertChatIdentityCurrent(identity)
  if (hydrateRequestVersions.get(requestKey) !== requestVersion) {
    return {
      status: response.session_status,
      hasRunningBackgroundAgents: response.has_running_background_agents,
      isSyntheticTurn: response.is_synthetic_turn,
      canSteer: response.can_steer,
    }
  }
  const eventsNow = chatStore.getTabEvents(sessionId)
  const concurrentEvents = eventsNow.filter(event => event.id && !startingIDs.has(event.id))
  // A provisional bubble survives only until its durable row is on the page.
  const restored = keepUnechoedProvisionals(response.events, eventsNow)
  restored.push(...pendingQueuedProvisionals(response.pending_messages, restored, sessionId))
  chatStore.setTabEvents(sessionId, resolveLiveInputConfirmations(restored))
  if (concurrentEvents.length > 0) appendRestoredLiveTail(sessionId, concurrentEvents)
  const cursor = response.latest_sequence ?? response.last_processed_index
  if (cursor !== undefined) chatStore.setTabLastEventIndex(sessionId, cursor)
  chatStore.setTabHasMoreOlderEvents(sessionId, response.has_more)
  chatStore.setTabHistoryPagination(sessionId, response.has_more && response.oldest_sequence
    ? { hasMore: true, nextOffset: response.oldest_sequence, ...(compact ? { compact: true } : {}) }
    : null)

  return {
    status: response.session_status,
    hasRunningBackgroundAgents: response.has_running_background_agents,
    isSyntheticTurn: response.is_synthetic_turn,
    canSteer: response.can_steer,
    restoredEvents: restored,
  }
}
