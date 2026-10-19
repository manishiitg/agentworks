// Reading a session's events without a store: one fetch of the polling
// route, and a follower that streams (fetch transport) with a polling
// fallback. AgentWorks keeps its own store-driven version of this; other
// apps use these directly.
import { SSEConnection, type SSELogger } from './sse'
import type { GetEventsResponse, SSEEventMessage, SSEStatusMessage } from './types'

export interface SessionClientConfig {
  baseUrl: string
  token: () => Promise<string | null> | string | null
  workingSet?: 'session' | 'all'
  durableChat?: boolean
  log?: SSELogger
}

/** GET /api/sessions/{id}/events in forward-polling mode. */
export async function fetchSessionEvents(cfg: SessionClientConfig, sessionId: string, since: number): Promise<GetEventsResponse> {
  const token = await cfg.token()
  const params = new URLSearchParams({ since: String(Math.max(since, 0)), working_set: cfg.workingSet ?? 'session' })
  if (cfg.durableChat) params.set('durable_chat', '1')
  const res = await fetch(`${cfg.baseUrl.replace(/\/+$/, '')}/api/sessions/${encodeURIComponent(sessionId)}/events?${params}`, {
    headers: token ? { Authorization: `Bearer ${token}` } : {},
  })
  if (!res.ok) throw new Error(`session events HTTP ${res.status}`)
  return (await res.json()) as GetEventsResponse
}

export class SessionEventsHTTPError extends Error {
  readonly status: number
  constructor(status: number) {
    super(`session events HTTP ${status}`)
    this.status = status
  }
}

/** GET the newest bounded page (or the page before `beforeSequence`) from the durable chat journal. */
export async function fetchRecentSessionEvents(cfg: SessionClientConfig, sessionId: string, limit = 300, beforeSequence?: number): Promise<GetEventsResponse> {
  const token = await cfg.token()
  const params = new URLSearchParams({ limit: String(limit), working_set: cfg.workingSet ?? 'session' })
  if (cfg.durableChat) params.set('durable_chat', '1')
  if (beforeSequence && beforeSequence > 0) params.set('before_sequence', String(beforeSequence))
  const res = await fetch(`${cfg.baseUrl.replace(/\/+$/, '')}/api/sessions/${encodeURIComponent(sessionId)}/events?${params}`, {
    headers: token ? { Authorization: `Bearer ${token}` } : {},
  })
  if (!res.ok) throw new SessionEventsHTTPError(res.status)
  return (await res.json()) as GetEventsResponse
}

/**
 * Pages the durable chat journal backwards until the oldest row, returning
 * events oldest-first. `maxPages` bounds a pathological history; `truncated`
 * reports when it cut the read short.
 */
export async function fetchCompleteSessionEvents(
  cfg: SessionClientConfig,
  sessionId: string,
  opts: { pageSize?: number; maxPages?: number } = {},
): Promise<{ events: GetEventsResponse['events']; truncated: boolean }> {
  const pageSize = opts.pageSize ?? 500
  const maxPages = opts.maxPages ?? 20
  const pages: GetEventsResponse['events'][] = []
  let before: number | undefined
  for (let page = 0; page < maxPages; page++) {
    const batch = await fetchRecentSessionEvents(cfg, sessionId, pageSize, before)
    pages.unshift(batch.events ?? [])
    const oldest = batch.oldest_sequence
    if (!batch.has_more || !oldest || oldest <= 1 || (before !== undefined && oldest >= before)) {
      return { events: dedupeEventsById(pages.flat()), truncated: false }
    }
    before = oldest
  }
  return { events: dedupeEventsById(pages.flat()), truncated: true }
}

function dedupeEventsById(events: GetEventsResponse['events']): GetEventsResponse['events'] {
  const seen = new Set<string>()
  return events.filter(event => {
    if (!event.id) return true
    if (seen.has(event.id)) return false
    seen.add(event.id)
    return true
  })
}

export interface FollowHandlers {
  /** Each "event" frame: a batch and the frame's store index. */
  onBatch: (batch: SSEEventMessage, frameIndex: number) => void
  onStatus?: (status: SSEStatusMessage) => void
  /** The follower gave up (stream failed repeatedly and polling failed too). */
  onEnd?: (err?: Error) => void
}

/**
 * followSession streams a session from `since`, falling back to polling
 * when the stream keeps failing. Returns a stop function.
 */
export function followSession(cfg: SessionClientConfig, sessionId: string, since: number, handlers: FollowHandlers, pollIntervalMs = 1000): () => void {
  let stopped = false
  let conn: SSEConnection | null = null
  let pollTimer: ReturnType<typeof setTimeout> | undefined
  let cursor = since

  const poll = async () => {
    if (stopped) return
    try {
      const batch = await fetchSessionEvents(cfg, sessionId, cursor)
      const next = typeof batch.last_processed_index === 'number' ? batch.last_processed_index : cursor
      if (batch.last_processed_index === -1) {
        // The server no longer knows this session in memory; nothing to follow.
        handlers.onEnd?.(new Error('session is not live'))
        return
      }
      handlers.onBatch({ events: batch.events, session_status: batch.session_status, display_status: batch.display_status, last_processed_index: next,
        has_running_background_agents: batch.has_running_background_agents, is_synthetic_turn: batch.is_synthetic_turn, can_steer: batch.can_steer, runtime_state: batch.runtime_state }, next)
      cursor = Math.max(cursor, next)
    } catch (err) {
      handlers.onEnd?.(err instanceof Error ? err : new Error(String(err)))
      return
    }
    if (!stopped) pollTimer = setTimeout(() => { void poll() }, pollIntervalMs)
  }

  const start = async () => {
    const token = await cfg.token()
    if (stopped) return
    conn = new SSEConnection({
      sessionId, sinceIndex: cursor, baseUrl: cfg.baseUrl, token, workingSet: cfg.workingSet, durableChat: cfg.durableChat, log: cfg.log,
      callbacks: {
        onMessage: (msg) => {
          const index = conn?.lastIndex ?? cursor
          cursor = Math.max(cursor, index)
          handlers.onBatch(msg, index)
        },
        onStatusUpdate: (msg) => handlers.onStatus?.(msg),
        onError: () => {
          if (stopped) return
          cfg.log?.warn('SSE', 'stream failed repeatedly; polling instead')
          void poll()
        },
      },
    })
  }
  void start()

  return () => {
    stopped = true
    conn?.close()
    if (pollTimer) clearTimeout(pollTimer)
  }
}
