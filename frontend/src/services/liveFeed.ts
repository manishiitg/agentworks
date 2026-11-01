// One live stream per tab (GET /api/live) for the header and the right pane.
// The server sends "X changed" notices, never data; panels refetch through
// their normal endpoints. Chat conversations keep their own per-session
// streams and never use this. See docs/design/live_update_feed.md.
import { getApiBaseUrl, getAuthToken } from './api'
import { readEventStream } from '../../shared/session/eventStream'

// Keep in sync with agent_go/internal/livefeed/livefeed.go.
export type LiveFeedKind = 'sessions' | 'schedules' | 'notifications' | 'human_inputs' | 'report' | 'plan' | 'ui_control' | 'browser'

export interface LiveFeedNotice {
  kind: LiveFeedKind
  workflow?: string
  // Chat session for session-scoped kinds (ui_control); sent to its owner only.
  session?: string
}

export type LiveFeedStatus = 'connecting' | 'live' | 'offline'

type Listener = {
  kinds: ReadonlySet<LiveFeedKind>
  workflow: string | null
  // notice is undefined on a resync (anything may have changed).
  onChange: (notice?: LiveFeedNotice) => void
}

// After this many consecutive failed connects the feed reports 'offline' so
// panels fall back to their timers, and retries slowly in the background.
const MAX_FAST_RETRIES = 3
const SLOW_RETRY_MS = 60_000

// "Workflow/<folder>/anything" -> "Workflow/<folder>" (and "Crew/<id>/…" ->
// "Crew/<id>" for shared crews), matching the server.
export function liveFeedWorkflowRoot(path: string | null | undefined): string | null {
  const parts = (path ?? '').trim().replace(/^\/+|\/+$/g, '').split('/')
  if (parts.length < 2 || (parts[0] !== 'Workflow' && parts[0] !== 'Crew') || !parts[1]) return null
  return `${parts[0]}/${parts[1]}`
}

class LiveFeed {
  private source: AbortController | null = null
  private listeners = new Set<Listener>()
  private statusListeners = new Set<(status: LiveFeedStatus) => void>()
  private status: LiveFeedStatus = 'offline'
  private failures = 0
  private everConnected = false
  private retryTimer: ReturnType<typeof setTimeout> | null = null

  getStatus(): LiveFeedStatus {
    return this.status
  }

  onStatus(cb: (status: LiveFeedStatus) => void): () => void {
    this.statusListeners.add(cb)
    return () => { this.statusListeners.delete(cb) }
  }

  subscribe(kinds: readonly LiveFeedKind[], workflow: string | null, onChange: (notice?: LiveFeedNotice) => void): () => void {
    const listener: Listener = { kinds: new Set(kinds), workflow: liveFeedWorkflowRoot(workflow) ?? workflow, onChange }
    this.listeners.add(listener)
    this.ensureOpen()
    return () => {
      this.listeners.delete(listener)
      if (this.listeners.size === 0) this.close()
    }
  }

  private setStatus(status: LiveFeedStatus) {
    if (this.status === status) return
    this.status = status
    for (const cb of this.statusListeners) cb(status)
  }

  private ensureOpen() {
    if (this.source || this.retryTimer || typeof fetch === 'undefined') return
    this.open()
  }

  private async open() {
    this.retryTimer = null
    if (this.listeners.size === 0) return
    const url = `${getApiBaseUrl().replace(/\/+$/, '')}/api/live`
    const token = getAuthToken()
    if (this.status !== 'offline' || !this.everConnected) this.setStatus('connecting')
    const source = new AbortController()
    this.source = source
    try {
      const response = await fetch(url, {
        headers: { Accept: 'text/event-stream', ...(token ? { Authorization: `Bearer ${token}` } : {}) },
        credentials: 'include',
        cache: 'no-store',
        signal: source.signal,
      })
      if (!response.ok || !response.body) throw new Error(`live stream HTTP ${response.status}`)
      await readEventStream(response.body, source.signal, ({ event, data }) => {
        if (event === 'resync') {
          this.failures = 0
          this.setStatus('live')
          // Even the first connection may follow failed initial reads while the
          // backend was starting. Every server resync invalidates panel snapshots.
          this.notifyAll()
          this.everConnected = true
        } else if (event === 'change') {
          let notice: LiveFeedNotice
          try { notice = JSON.parse(data) } catch { return }
          for (const listener of this.listeners) {
            if (!listener.kinds.has(notice.kind)) continue
            if (notice.workflow && listener.workflow && listener.workflow !== notice.workflow) continue
            listener.onChange(notice)
          }
        }
      })
    } catch {
      // The existing panels poll while offline; retry the stream below.
    } finally {
      if (this.source === source && !source.signal.aborted) {
        source.abort()
        this.source = null
        this.failures += 1
        const offline = this.failures >= MAX_FAST_RETRIES
        this.setStatus(offline ? 'offline' : 'connecting')
        this.retryTimer = setTimeout(() => { void this.open() }, offline ? SLOW_RETRY_MS : 1000)
      }
    }
  }

  private notifyAll() {
    for (const listener of this.listeners) listener.onChange()
  }

  private close() {
    if (this.retryTimer) clearTimeout(this.retryTimer)
    this.retryTimer = null
    this.source?.abort()
    this.source = null
    this.failures = 0
    this.setStatus('offline')
  }
}

export const liveFeed = new LiveFeed()
