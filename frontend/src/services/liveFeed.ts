// One live stream per tab (GET /api/live) for the header and the right pane.
// The server sends "X changed" notices, never data; panels refetch through
// their normal endpoints. Chat conversations keep their own per-session
// streams and never use this. See docs/design/live_update_feed.md.
import { getApiBaseUrl, getAuthToken } from './api'

// Keep in sync with agent_go/internal/livefeed/livefeed.go.
export type LiveFeedKind = 'sessions' | 'schedules' | 'notifications' | 'human_inputs' | 'report'

export interface LiveFeedNotice {
  kind: LiveFeedKind
  workflow?: string
}

export type LiveFeedStatus = 'connecting' | 'live' | 'offline'

type Listener = {
  kinds: ReadonlySet<LiveFeedKind>
  workflow: string | null
  onChange: () => void
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
  private source: EventSource | null = null
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

  subscribe(kinds: readonly LiveFeedKind[], workflow: string | null, onChange: () => void): () => void {
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
    if (this.source || this.retryTimer || typeof EventSource === 'undefined') return
    this.open()
  }

  private open() {
    this.retryTimer = null
    if (this.listeners.size === 0) return
    const params = new URLSearchParams()
    const token = getAuthToken()
    if (token) params.set('token', token)
    const query = params.toString()
    const url = `${getApiBaseUrl().replace(/\/+$/, '')}/api/live${query ? `?${query}` : ''}`
    if (this.status !== 'offline' || !this.everConnected) this.setStatus('connecting')
    const source = new EventSource(url)
    this.source = source

    source.addEventListener('resync', () => {
      this.failures = 0
      this.setStatus('live')
      // The first resync only confirms the stream: panels have just loaded
      // their data. After a reconnect anything may have been missed.
      if (this.everConnected) this.notifyAll()
      this.everConnected = true
    })
    source.addEventListener('change', (event) => {
      let notice: LiveFeedNotice
      try {
        notice = JSON.parse((event as MessageEvent<string>).data)
      } catch {
        return
      }
      for (const listener of this.listeners) {
        if (!listener.kinds.has(notice.kind)) continue
        if (notice.workflow && listener.workflow && listener.workflow !== notice.workflow) continue
        listener.onChange()
      }
    })
    source.onerror = () => {
      // EventSource retries by itself after a dropped stream; only a refused
      // or repeatedly failing connection needs our fallback.
      if (source.readyState !== EventSource.CLOSED && this.status === 'live') {
        this.setStatus('connecting')
        return
      }
      this.failures += 1
      if (source.readyState === EventSource.CLOSED || this.failures >= MAX_FAST_RETRIES) {
        source.close()
        this.source = null
        this.setStatus('offline')
        this.retryTimer = setTimeout(() => this.open(), SLOW_RETRY_MS)
      }
    }
  }

  private notifyAll() {
    for (const listener of this.listeners) listener.onChange()
  }

  private close() {
    if (this.retryTimer) clearTimeout(this.retryTimer)
    this.retryTimer = null
    this.source?.close()
    this.source = null
    this.failures = 0
    this.setStatus('offline')
  }
}

export const liveFeed = new LiveFeed()
