type Result = { isError?: boolean; content?: { type: string; text?: string }[]; structuredContent?: Record<string, unknown>; _meta?: Record<string, unknown> }
type HostContext = { theme?: 'dark' | 'light' }

/** Standard MCP Apps bridge. The host retains the connection's credentials. */
export class DashboardHostBridge {
  private next = 0
  private parentOrigin: string | undefined
  private pending = new Map<number, { resolve: (value: unknown) => void; reject: (reason: Error) => void; timer: ReturnType<typeof setTimeout> }>()
  onResult: (value: Result) => void = () => {}
  onContext: (value: HostContext) => void = () => {}

  constructor() {
    window.addEventListener('message', event => {
      if (event.source !== window.parent || event.data?.jsonrpc !== '2.0') return
      if (this.parentOrigin && event.origin !== this.parentOrigin) return
      const data = event.data
      if (typeof data.id === 'number' && this.pending.has(data.id)) {
        if (event.origin !== 'null') this.parentOrigin = event.origin
        const pending = this.pending.get(data.id)!
        clearTimeout(pending.timer)
        this.pending.delete(data.id)
        if (data.error) pending.reject(new Error(data.error.message || 'The host could not complete the request.'))
        else pending.resolve(data.result)
      } else if (data.method === 'ui/notifications/tool-result') this.onResult(data.params)
      else if (data.method === 'ui/notifications/host-context-changed') this.onContext(data.params)
      else if (data.method === 'ping') window.parent.postMessage({ jsonrpc: '2.0', id: data.id, result: {} }, '*')
      else if (data.method === 'ui/resource-teardown') {
        window.parent.postMessage({ jsonrpc: '2.0', id: data.id, result: {} }, '*')
        for (const pending of this.pending.values()) { clearTimeout(pending.timer); pending.reject(new Error('Dashboard closed.')) }
        this.pending.clear()
      }
    })
  }

  request(method: string, params: unknown): Promise<unknown> {
    const id = ++this.next
    return new Promise((resolve, reject) => {
      const timer = setTimeout(() => { this.pending.delete(id); reject(new Error('The chat host did not answer. Open the dashboard website link.')) }, 90_000)
      this.pending.set(id, { resolve, reject, timer })
      window.parent.postMessage({ jsonrpc: '2.0', id, method, params }, '*')
    })
  }

  async connect() {
    const result = await this.request('ui/initialize', { appInfo: { name: 'AgentWorks dashboard', version: '1.0.0' }, appCapabilities: {}, protocolVersion: '2026-01-26' }) as { hostContext?: HostContext }
    this.onContext(result.hostContext ?? {})
    this.notify('ui/notifications/initialized', {})
  }

  notify(method: string, params: unknown) { window.parent.postMessage({ jsonrpc: '2.0', method, params }, '*') }

  async tool(name: string, args: Record<string, unknown>): Promise<Result> {
    const result = await this.request('tools/call', { name, arguments: args }) as Result
    if (result.isError) throw new Error(result.content?.find(item => item.type === 'text')?.text || 'This action is unavailable. Open AgentWorks.')
    return result
  }

  async call(name: string, args: Record<string, unknown>): Promise<Record<string, unknown>> {
    const result = await this.tool('call_tool', { name, arguments: args })
    if (result.structuredContent) return result.structuredContent
    return JSON.parse(result.content?.find(item => item.type === 'text')?.text || '{}') as Record<string, unknown>
  }
}

export function resultData(result: Result): Record<string, unknown> {
  return result.structuredContent ?? JSON.parse(result.content?.find(item => item.type === 'text')?.text || '{}') as Record<string, unknown>
}
