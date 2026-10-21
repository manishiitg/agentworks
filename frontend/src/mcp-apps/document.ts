import type { ReportDataApi } from '../components/workflow/reportWidgets/reportEmbedContext'
import { applyReportDocumentTheme, installReportDocumentHost } from '../components/workflow/reportWidgets/reportHostRuntime'
import { renderReportMarkdown } from '../components/workflow/reportWidgets/reportMarkdown'

type Configuration = { channel: string; title: string; workspacePath: string; theme: 'dark' | 'light'; canAsk: boolean }
const config = JSON.parse(document.getElementById('aw-document-config')!.textContent!) as Configuration
let sequence = 0
let parentOrigin: string | undefined
const pending = new Map<number, { resolve: (value: unknown) => void; reject: (reason: Error) => void }>()
function request(method: string, args: unknown[] = []): Promise<unknown> {
  const id = ++sequence
  return new Promise((resolve, reject) => {
    pending.set(id, { resolve, reject })
    window.parent.postMessage({ channel: config.channel, id, method, args }, '*')
  })
}
window.addEventListener('message', event => {
  if (event.source !== window.parent || event.data?.channel !== config.channel) return
  if (parentOrigin && event.origin !== parentOrigin) return
  if (event.origin !== 'null') parentOrigin = event.origin
  if (event.data.method === 'theme') { applyReportDocumentTheme(document, null, event.data.theme === 'dark' ? 'dark' : 'light', true); return }
  const item = pending.get(event.data.id)
  if (!item) return
  pending.delete(event.data.id)
  if (event.data.error) item.reject(new Error(event.data.error))
  else item.resolve(event.data.value)
})
const unavailable = async () => { throw new Error('Open the dashboard in AgentWorks for scripts or database changes. Use this dashboard’s Ask and published action buttons.') }
async function file(path: string) {
  const data = await request('file', [path]) as { content_base64: string; mime_type: string }
  const bytes = Uint8Array.from(atob(data.content_base64), char => char.charCodeAt(0))
  return { bytes, type: data.mime_type }
}
const api: ReportDataApi = {
  workspacePath: config.workspacePath,
  query: async (sql, params) => {
    const value = await request('query', [sql, params]) as { success?: boolean; error?: string; data?: { rows?: Record<string, unknown>[] } }
    if (!value.success) throw new Error(value.error || 'The dashboard query failed.')
    return value.data?.rows ?? []
  },
  getText: async path => new TextDecoder().decode((await file(path)).bytes),
  get: async path => {
    const text = new TextDecoder().decode((await file(path)).bytes)
    try { return JSON.parse(text) } catch { return text }
  },
  getHtml: async path => renderReportMarkdown(new TextDecoder().decode((await file(path)).bytes)),
  renderMarkdown: renderReportMarkdown,
  fileUrl: async path => { const data = await file(path); return URL.createObjectURL(new Blob([data.bytes], { type: data.type })) },
  mediaUrl: async () => { throw new Error('Open the website dashboard to play streamed media.') },
  openFile: path => { void request('file.open', [path]) },
  getCosts: async options => await request('costs', [options]) as Awaited<ReturnType<NonNullable<ReportDataApi['getCosts']>>>,
  sendChatMessage: async message => { await request('chat.open', [message]); throw new Error('Your message is in Ask. Press Send to submit it.') },
  updateField: unavailable,
  updateFields: unavailable,
  run: unavailable,
}

function start() {
  installReportDocumentHost(document, {
    title: config.title, dataApi: api, theme: config.theme, tokenSource: null, dispatchData: true,
    chatApi: {
      available: config.canAsk,
      open: async () => { await request('chat.open') },
      close: () => { void request('chat.close') },
      mount: async selector => {
        const target = document.querySelector(selector)
        if (!target) return
        const button = document.createElement('button')
        button.textContent = 'Ask about this dashboard'
        button.type = 'button'
        button.disabled = !config.canAsk
        button.onclick = () => { void request('chat.open') }
        target.replaceChildren(button)
      },
    },
  })
  document.addEventListener('click', event => {
    const button = (event.target as Element | null)?.closest('[data-report-action]')
    if (!button) return
    event.preventDefault()
    let defaults: unknown = {}
    try { defaults = JSON.parse(button.getAttribute('data-report-action-defaults') || '{}') } catch { /* optional defaults */ }
    void request('function.open', [button.getAttribute('data-report-action'), defaults])
  })
  document.addEventListener('click', event => {
    const link = (event.target as Element | null)?.closest<HTMLAnchorElement>('a[href]')
    if (link && /^(https?:|mailto:)/i.test(link.getAttribute('href') || '')) {
      event.preventDefault(); event.stopImmediatePropagation(); void request('link.open', [link.href])
    }
  }, true)
  new ResizeObserver(() => { void request('size', [Math.max(220, document.documentElement.scrollHeight)]) }).observe(document.body)
}
if (document.readyState === 'loading') document.addEventListener('DOMContentLoaded', start, { once: true })
else start()
