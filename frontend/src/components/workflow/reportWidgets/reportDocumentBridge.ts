/** The opaque document transport used by website dashboards, previews and MCP Apps. */
export function isReportDocumentMessage(event: MessageEvent, child: Window | null, channel: string): boolean {
  return Boolean(child && event.source === child && event.data?.channel === channel)
}

type Handler = (...args: unknown[]) => unknown

export class ReportDocumentBridge {
  private sequence = 0
  private handlers = new Map<string, Handler>()
  private pending = new Map<number, { resolve: (value: unknown) => void; reject: (error: Error) => void; timer: ReturnType<typeof setTimeout> }>()
  private disposed = false

  private target: () => Window | null
  private channel: string
  constructor(target: () => Window | null, channel: string) {
    this.target = target; this.channel = channel
    window.addEventListener('message', this.receive)
  }

  private receive = async (event: MessageEvent) => {
    if (this.disposed || !isReportDocumentMessage(event, this.target(), this.channel)) return
    const { id, method, args, value, error } = event.data
    if (typeof method !== 'string') {
      const pending = this.pending.get(id)
      if (!pending) return
      clearTimeout(pending.timer); this.pending.delete(id)
      if (typeof error === 'string') pending.reject(new Error(error))
      else pending.resolve(value)
      return
    }
    const destination = event.source as Window
    try {
      const handler = this.handlers.get(method)
      if (!handler || !Array.isArray(args)) throw new Error('This dashboard operation is unavailable.')
      const result = await handler(...args)
      if (!this.disposed && typeof id === 'number') destination.postMessage({ channel: this.channel, id, value: result }, '*')
    } catch (reason) {
      if (!this.disposed && typeof id === 'number') destination.postMessage({ channel: this.channel, id, error: reason instanceof Error ? reason.message : 'The dashboard operation failed.' }, '*')
    }
  }

  handle(method: string, handler: Handler): () => void {
    this.handlers.set(method, handler)
    return () => { if (this.handlers.get(method) === handler) this.handlers.delete(method) }
  }

  request(method: string, args: unknown[] = []): Promise<unknown> {
    if (this.disposed) return Promise.reject(new Error('This dashboard document changed.'))
    const id = ++this.sequence
    return new Promise((resolve, reject) => {
      const timer = setTimeout(() => { this.pending.delete(id); reject(new Error('The dashboard did not answer. Try again.')) }, 90_000)
      this.pending.set(id, { resolve, reject, timer })
      this.target()?.postMessage({ channel: this.channel, id, method, args }, '*')
    })
  }

  notify(method: string, args: unknown[] = []) {
    if (!this.disposed) this.target()?.postMessage({ channel: this.channel, method, args }, '*')
  }

  dispose() {
    this.disposed = true
    window.removeEventListener('message', this.receive)
    for (const pending of this.pending.values()) { clearTimeout(pending.timer); pending.reject(new Error('This dashboard document changed.')) }
    this.pending.clear(); this.handlers.clear()
  }
}
