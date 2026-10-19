// The agent server's session event stream, for any app.
//
// Fetch streaming keeps sign-in tokens in the Authorization header, never
// in request URLs. Browser and other app clients parse the same named
// frames: "event" (a batch of events plus the session's runtime state) and
// "status" (state only). The SSE id of each frame is the store index to
// resume from.
import type { SSEEventMessage, SSEStatusMessage } from './types'
import { readEventStream } from './eventStream'

export interface SSECallbacks {
  onMessage: (msg: SSEEventMessage) => void
  onStatusUpdate: (msg: SSEStatusMessage) => void
  onError?: (error: Event) => void
  onOpen?: () => void
}

export interface SSELogger {
  debug(scope: string, ...args: unknown[]): void
  warn(scope: string, ...args: unknown[]): void
  error(scope: string, ...args: unknown[]): void
}

export interface SSEOptions {
  sessionId: string
  /** Store index to resume from; negative means "from the start". */
  sinceIndex: number
  callbacks: SSECallbacks
  baseUrl: string
  /** Bearer token sent only in the Authorization header. */
  token?: string | null
  workingSet?: 'session' | 'all'
  /** Resume chat from the durable SQLite sequence log, not the live window. */
  durableChat?: boolean
  maxConsecutiveErrors?: number
  log?: SSELogger
}

const silent: SSELogger = { debug: () => {}, warn: () => {}, error: () => {} }

export class SSEConnection {
  private abort: AbortController | null = null
  private sinceIndex: number
  private consecutiveErrors = 0
  private readonly maxConsecutiveErrors: number
  private closed = false
  private open = false
  private readonly opts: SSEOptions
  private readonly log: SSELogger

  constructor(opts: SSEOptions) {
    this.opts = opts
    this.sinceIndex = opts.sinceIndex
    this.maxConsecutiveErrors = opts.maxConsecutiveErrors ?? 5
    this.log = opts.log ?? silent
    this.connect()
  }

  private url(): string {
    const params = new URLSearchParams()
    // Detailed child transcripts are fetched only for the selected terminal.
    params.set('working_set', this.opts.workingSet ?? 'session')
    if (this.opts.durableChat) params.set('durable_chat', '1')
    if (this.sinceIndex >= 0) params.set('since', String(this.sinceIndex))
    return `${this.opts.baseUrl.replace(/\/+$/, '')}/api/sessions/${encodeURIComponent(this.opts.sessionId)}/events/stream?${params}`
  }

  private connect() {
    if (this.closed) return
    void this.connectFetch()
  }

  private handleEventFrame(data: string, lastEventId: string | number | undefined) {
    try {
      const msg: SSEEventMessage = JSON.parse(data)
      if (lastEventId !== undefined && lastEventId !== '') {
        const n = typeof lastEventId === 'number' ? lastEventId : parseInt(lastEventId, 10)
        if (!Number.isNaN(n)) this.sinceIndex = n
      }
      this.opts.callbacks.onMessage(msg)
    } catch (err) {
      this.log.error('SSE', 'Failed to parse event message:', err)
    }
  }

  private handleStatusFrame(data: string) {
    try {
      this.opts.callbacks.onStatusUpdate(JSON.parse(data) as SSEStatusMessage)
    } catch (err) {
      this.log.error('SSE', 'Failed to parse status message:', err)
    }
  }

  private failure(e: Event) {
    this.consecutiveErrors++
    this.log.warn('SSE', `Connection error for session ${this.opts.sessionId} (${this.consecutiveErrors}/${this.maxConsecutiveErrors})`)
    if (this.consecutiveErrors >= this.maxConsecutiveErrors) {
      this.log.error('SSE', `Too many consecutive errors for session ${this.opts.sessionId}, triggering fallback`)
      this.close()
      this.opts.callbacks.onError?.(e)
      return false
    }
    return true
  }

  private async connectFetch() {
    while (!this.closed) {
      const controller = new AbortController()
      this.abort = controller
      try {
        const res = await fetch(this.url(), {
          headers: { Accept: 'text/event-stream', ...(this.opts.token ? { Authorization: `Bearer ${this.opts.token}` } : {}) },
          signal: controller.signal,
          credentials: 'include',
          cache: 'no-store',
        })
        if (!res.ok || !res.body) throw new Error(`event stream HTTP ${res.status}`)
        this.consecutiveErrors = 0
        this.open = true
        this.log.debug('SSE', `Connected to session ${this.opts.sessionId}`)
        this.opts.callbacks.onOpen?.()
        await readEventStream(res.body, controller.signal, ({ event, data, id }) => {
          if (event === 'event' || event === '') this.handleEventFrame(data, id)
          else if (event === 'status') this.handleStatusFrame(data)
          else if (event === 'cursor' && id !== undefined) {
            const cursor = Number.parseInt(id, 10)
            if (!Number.isNaN(cursor)) this.sinceIndex = cursor
          }
        })
        this.open = false
        if (this.closed) return
        // The server ended the stream; resume from the last id like EventSource would.
      } catch (err) {
        this.open = false
        if (this.closed) return
        if (!this.failure(new Event('error'))) return
        this.log.debug('SSE', `retrying after: ${err instanceof Error ? err.message : String(err)}`)
      }
      await new Promise((r) => setTimeout(r, 1000 * Math.min(this.consecutiveErrors + 1, 5)))
    }
  }

  /** Close the connection permanently. */
  close() {
    this.closed = true
    this.open = false
    if (this.abort) {
      this.abort.abort()
      this.abort = null
    }
    this.log.debug('SSE', `Closed connection for session ${this.opts.sessionId}`)
  }

  /** The store index of the last frame received (what a reconnect resumes from). */
  get lastIndex(): number {
    return this.sinceIndex
  }

  /** Whether the connection is currently open. */
  get isConnected(): boolean {
    return this.open
  }

  /** Whether the connection has been permanently closed. */
  get isClosed(): boolean {
    return this.closed
  }
}
