import { createReportQuery } from '../components/workflow/reportWidgets/reportQuery'
// Headless preview page for a workflow report (preview_report tool).
//
// Served by the Go server at /report-preview/ (embedded), loaded by a headless
// browser with ?workspace=<Workflow/x>&token=<short-lived preview token>. It
// renders the requested db/reports/*.html document through the SAME host runtime the in-app Report
// tab uses (reportHostRuntime.ts) -- bootstrap stub, window.report, theme,
// error surface -- so what the tool observes is what the user sees. Data goes
// through two read-only endpoints on the Go server that accept the preview
// token; nothing here touches the workspace service or the app's stores.
//
// The page exposes, for the tool to poll via `agent-browser eval`:
//   document.documentElement.dataset.previewState   'loading' | 'ready' | 'error' | 'missing' | 'failed'
//   window.__reportPreview.getState()               -> JSON-able snapshot
//   window.__reportPreview.setTheme('dark'|'light') -> re-theme without reload
//   window.__reportPreview.setWidth(px)             -> re-layout at a viewport width

import type { WorkflowCostsResponse } from '../services/api-types'
import type { ReportDataApi } from '../components/workflow/reportWidgets/reportEmbedContext'
import { reportMotionCapture } from '../components/workflow/reportWidgets/reportMotion'
import { captureReportData } from './exportSnapshot'
import { staticAssetDataApi, staticDataApi, staticIndex, type StaticSnapshot } from './staticSnapshot'
import { inspectReportUI } from './uiChecks'
import {
  applyReportTheme,
  installReportHost,
  readReportHostState,
  withReportBootstrap,
  type ReportHostTheme,
} from '../components/workflow/reportWidgets/reportHostRuntime'
import {
  allowedReportPath,
  renderReportMarkdown,
  reportMarkdownBasePath,
} from '../components/workflow/reportWidgets/reportMarkdown'

// The app palette, verbatim from index.css, so hsl(var(--token)) resolves in
// the preview exactly as it does in the app. Kept here (not fetched) because
// the preview must not depend on the SPA bundle being served.
const APP_TOKENS_CSS = `
:root{--background:0 0% 100%;--foreground:0 0% 20%;--card:0 0% 98%;--card-foreground:0 0% 20%;--popover:0 0% 98%;--popover-foreground:0 0% 20%;--primary:200 100% 40%;--primary-foreground:0 0% 100%;--secondary:0 0% 96%;--secondary-foreground:0 0% 20%;--muted:0 0% 96%;--muted-foreground:0 0% 45%;--accent:200 100% 40%;--accent-foreground:0 0% 100%;--destructive:0 70% 60%;--destructive-foreground:0 0% 100%;--border:0 0% 85%;--input:0 0% 96%;--ring:200 100% 40%;--chart-1:200 100% 40%;--chart-2:180 60% 50%;--chart-3:20 60% 60%;--chart-4:280 60% 60%;--chart-5:0 70% 60%}
:root.dark{--background:220 13% 6%;--foreground:220 16% 91%;--card:220 12% 9%;--card-foreground:220 16% 91%;--popover:220 12% 8%;--popover-foreground:220 16% 91%;--primary:200 100% 40%;--primary-foreground:0 0% 100%;--secondary:220 10% 13%;--secondary-foreground:220 16% 91%;--muted:220 10% 13%;--muted-foreground:220 9% 66%;--accent:200 100% 40%;--accent-foreground:0 0% 100%;--destructive:0 70% 60%;--destructive-foreground:0 0% 100%;--border:220 9% 18%;--input:220 10% 12%;--ring:200 100% 40%;--chart-1:200 100% 40%;--chart-2:180 60% 50%;--chart-3:20 60% 60%;--chart-4:280 60% 60%;--chart-5:0 70% 60%}
html,body{margin:0;padding:0;background:hsl(var(--background));color:hsl(var(--foreground));font-family:system-ui,-apple-system,sans-serif}
#report-preview-shell{margin:0 auto;width:100%}
#report-preview-frame{display:block;width:100%;border:0}
#report-preview-status{padding:12px 14px;font:13px/1.5 ui-monospace,SFMono-Regular,Menlo,monospace}
`

type PreviewLifecycle = 'loading' | 'ready' | 'error' | 'missing' | 'failed'

interface PreviewSnapshot {
  previewState: PreviewLifecycle
  workspace: string
  theme: ReportHostTheme
  width: number
  report: ReturnType<typeof readReportHostState>
  openedFiles: string[]
  consoleErrors: string[]
  fetchErrors: string[]
}

const savedSnapshot = document.getElementById('agentworks-static-snapshot')?.textContent
const staticSnapshot: StaticSnapshot | null = savedSnapshot ? JSON.parse(savedSnapshot) : null
const params = new URLSearchParams(window.location.search)
const staticCapture = params.get('static') === '1'
let sourceHTML = ''
const workspace = (params.get('workspace') || '').replace(/^\/+|\/+$/g, '')
const token = params.get('token') || ''
const requestedDocument = params.get('document') || 'db/reports/index.html'
const documentPath = /^db\/reports\/(?!.*(?:^|\/)\.\.(?:\/|$))[^\\]+\.html$/i.test(requestedDocument)
  ? requestedDocument
  : 'db/reports/index.html'
const initialTheme: ReportHostTheme = staticSnapshot?.theme ?? (params.get('theme') === 'light' ? 'light' : 'dark')
const initialWidth = Math.max(320, Number(params.get('width')) || 1280)
const requestedHeight = Number(params.get('height'))
const exportHeight = params.get('export') === '1' && Number.isFinite(requestedHeight) && requestedHeight > 0 ? requestedHeight : 0

const openedFiles: string[] = []
const consoleErrors: string[] = []
const fetchErrors: string[] = []
let frame: HTMLIFrameElement | null = null
let currentTheme: ReportHostTheme = initialTheme
let exportCapture: ReturnType<typeof captureReportData> | undefined

// "ResizeObserver loop completed with undelivered notifications." is a
// Chromium-only diagnostic, not a real failure: it fires whenever a
// ResizeObserver callback itself triggers another layout change within the
// same frame, which report dashboards full of auto-sizing widgets do
// constantly. Left uncaptured, every clean report review carries this noise
// in page_errors, forcing every reviewer (human or agent) to re-derive that
// it's benign instead of preview_report ever being able to say so itself.
function isBenignWindowError(message: string): boolean {
  return message.includes('ResizeObserver loop completed with undelivered notifications')
}

function setPreviewState(state: PreviewLifecycle) {
  document.documentElement.setAttribute('data-preview-state', state)
}

function setStatus(text: string) {
  const el = document.getElementById('report-preview-status')
  if (el) el.textContent = text
}

function apiUrl(path: string, query: Record<string, string>): string {
  const search = new URLSearchParams({ ...query, workspace, token })
  return `/api/workflow/report-preview/${path}?${search.toString()}`
}

async function fetchFile(path: string): Promise<{ ok: boolean; status: number; text: string }> {
  const allowed = allowedReportPath(path)
  if (!allowed) return { ok: false, status: 400, text: '' }
  try {
    const response = await fetch(apiUrl('file', { path: allowed }), { headers: { Authorization: `Bearer ${token}` } })
    const text = await response.text()
    if (!response.ok) fetchErrors.push(`${allowed}: HTTP ${response.status}`)
    return { ok: response.ok, status: response.status, text }
  } catch (error) {
    fetchErrors.push(`${allowed}: ${String(error)}`)
    return { ok: false, status: 0, text: '' }
  }
}

async function fetchReportMetrics<T>(kind: string, params: Record<string, string> = {}): Promise<T> {
  const response = await fetch(apiUrl(kind, params), { headers: { Authorization: `Bearer ${token}` } })
  if (!response.ok) throw new Error(`${kind} unavailable (HTTP ${response.status})`)
  return response.json() as Promise<T>
}

function createPreviewDataApi(): ReportDataApi {
  return {
    workspacePath: workspace,
    getRelayRuns: (options = {}) => fetchReportMetrics('relay-runs', { version: options.version || 'draft', limit: String(options.limit || 20) }),
    getCosts: options => fetchReportMetrics<WorkflowCostsResponse>('costs', { days: String(options.days || 30), before: options.before || '' }),
    query: createReportQuery(workspace, async body => {
      const response = await fetch(apiUrl('query', {}), {
        method: 'POST',
        headers: { 'Content-Type': 'application/json', Authorization: `Bearer ${token}` },
        body: JSON.stringify(body),
      })
      const result = await response.json().catch(() => ({}))
      if (!response.ok && !result.error) throw new Error(`Query failed (HTTP ${response.status}).`)
      return result
    }),
    getText: async (path: string) => {
      const result = await fetchFile(path)
      return result.ok ? result.text : null
    },
    get: async (path: string) => {
      const result = await fetchFile(path)
      if (!result.ok || !result.text.trim()) return null
      try { return JSON.parse(result.text) } catch { return result.text }
    },
    renderMarkdown: (markdown: string) => renderReportMarkdown(markdown),
    getHtml: async (path: string) => {
      const result = await fetchFile(path)
      return result.ok ? renderReportMarkdown(result.text, reportMarkdownBasePath(path)) || null : null
    },
    // The file endpoint accepts the token as a query parameter, so the URL is
    // usable directly in <img src> / <iframe src> without a blob round-trip.
    mediaUrl: async (path: string) => {
      const response = await fetch(apiUrl('media-url', {}), {
        method: 'POST', headers: { 'Content-Type': 'application/json', Authorization: `Bearer ${token}` },
        body: JSON.stringify({ workspace, path }),
      })
      if (!response.ok) throw new Error((await response.text()).trim() || `Media unavailable (HTTP ${response.status})`)
      const body = await response.json() as { url: string }
      return new URL(body.url, window.location.href).href
    },
    fileUrl: async (path: string) => {
      const allowed = allowedReportPath(path)
      return allowed ? apiUrl('file', { path: allowed }) : null
    },
    openFile: (path: string) => { openedFiles.push(path) },
    updateField: async () => { throw new Error('window.report.updateField is not available in the preview') },
    updateFields: async () => { throw new Error('window.report.updateFields is not available in the preview') },
    run: async (path: string, args?: unknown) => {
      const response = await fetch(apiUrl('run', {}), {
        method: 'POST',
        headers: { 'Content-Type': 'application/json', Authorization: `Bearer ${token}` },
        body: JSON.stringify({ workspace, path, args: args ?? {} }),
      })
      const text = await response.text()
      let body: { success?: boolean; data?: unknown; error?: string; stderr?: string } = {}
      try { body = JSON.parse(text) } catch { /* plain-text error */ }
      if (!response.ok || !body.success) {
        const message = body.error || text.trim() || `Script failed (HTTP ${response.status}).`
        throw new Error(body.stderr ? `${message}\n${body.stderr}` : message)
      }
      return body.data
    },
    sendChatMessage: async () => { throw new Error('window.report.sendChatMessage is not available in the preview. Open the report in the app to send a message.') },
  }
}

function snapshot(): PreviewSnapshot {
  return {
    previewState: (document.documentElement.getAttribute('data-preview-state') as PreviewLifecycle) || 'loading',
    workspace,
    theme: currentTheme,
    width: frame ? frame.getBoundingClientRect().width : initialWidth,
    report: readReportHostState(frame),
    openedFiles: [...openedFiles],
    consoleErrors: [...consoleErrors],
    fetchErrors: [...fetchErrors],
  }
}

function resizeFrame() {
  const doc = frame?.contentDocument
  if (!frame || !doc?.body) return
  if (exportHeight) return
  if (doc.documentElement.getAttribute('data-report-fit') === 'viewport') {
    const available = window.innerHeight - Math.max(0, frame.getBoundingClientRect().top)
    if (available > 0) frame.style.height = `${Math.floor(available)}px`
    return
  }
  const scrollY = doc.defaultView?.scrollY ?? 0
  let maxBottom = 0
  for (const child of Array.from(doc.body.children)) {
    const bottom = child.getBoundingClientRect().bottom + scrollY
    if (bottom > maxBottom) maxBottom = bottom
  }
  if (maxBottom > 0) frame.style.height = `${Math.ceil(maxBottom)}px`
}

function mirrorLifecycle() {
  const doc = frame?.contentDocument
  if (!doc?.documentElement) return
  const state = doc.documentElement.getAttribute('data-report-state')
  if (state === 'ready' || state === 'error') setPreviewState(state)
}

function setTheme(theme: ReportHostTheme) {
  currentTheme = theme
  document.documentElement.classList.toggle('dark', theme === 'dark')
  document.documentElement.setAttribute('data-theme', theme)
  if (frame) applyReportTheme(frame, document.documentElement, theme, true)
}

function setWidth(width: number) {
  const shell = document.getElementById('report-preview-shell')
  if (shell) shell.style.maxWidth = `${Math.max(320, width)}px`
  window.setTimeout(resizeFrame, 50)
}

async function start() {
  window.addEventListener('resize', resizeFrame)
  const style = document.createElement('style')
  style.textContent = APP_TOKENS_CSS
  document.head.appendChild(style)
  document.title = 'Dashboard preview'
  document.body.innerHTML = '<div id="report-preview-shell"><div id="report-preview-status">Loading dashboard…</div></div>'
  setPreviewState('loading')
  setTheme(initialTheme)
  setWidth(initialWidth)

  window.addEventListener('error', (e) => {
    const message = String((e as ErrorEvent).message || 'error')
    if (isBenignWindowError(message)) return
    consoleErrors.push(message)
  })
  window.addEventListener('unhandledrejection', (e) => consoleErrors.push(String((e as PromiseRejectionEvent).reason?.message || e.reason || 'rejection')))
  ;(window as unknown as { __reportPreview: unknown }).__reportPreview = {
    getState: snapshot, setTheme, setWidth,
    exportStatic: async (liveURL: string) => {
      if (!staticCapture || snapshot().previewState !== 'ready') throw new Error('Load the dashboard successfully before exporting.')
      const data = await exportCapture!.freeze()
      const html = sourceHTML.replaceAll('/dashboard-react.js', './dashboard-react.js').replaceAll('/dashboard.css', './dashboard.css')
      return staticIndex({ version: 1, html, data, as_of: new Date().toISOString(), theme: currentTheme, live_url: liveURL })
    },
    getUIChecks: () => frame?.contentDocument ? inspectReportUI(frame.contentDocument, window.innerWidth) : { error: 'Dashboard document is unavailable.' },
    motion: () => frame?.contentDocument ? reportMotionCapture(frame.contentDocument) : [],
    seek: (seconds: number) => frame?.contentDocument ? reportMotionCapture(frame.contentDocument, seconds) : [],
    freeze: async () => {
      await frame?.contentDocument?.fonts.ready
      return exportCapture?.freeze()
    },
  }

  if (!staticSnapshot && (!workspace || !token)) {
    setStatus('Missing workspace or token.')
    setPreviewState('failed')
    return
  }

  const report = staticSnapshot ? { ok: true, status: 200, text: staticSnapshot.html } : await fetchFile(documentPath)
  sourceHTML = report.text
  if (!report.ok) {
    setStatus(report.status === 404 ? `No ${documentPath} in this workspace.` : `Could not load the report (HTTP ${report.status}).`)
    setPreviewState(report.status === 404 ? 'missing' : 'failed')
    return
  }

  const shell = document.getElementById('report-preview-shell')!
  shell.innerHTML = ''
  frame = document.createElement('iframe')
  frame.id = 'report-preview-frame'
  frame.title = 'Dashboard preview'
  if (exportHeight) frame.style.height = `${exportHeight}px`
  frame.setAttribute('sandbox', 'allow-same-origin allow-scripts')
  const liveApi = createPreviewDataApi()
  exportCapture = captureReportData(staticCapture ? staticAssetDataApi(liveApi) : liveApi, params.get('export') === '1', staticCapture)
  const dataApi = staticSnapshot ? staticDataApi(staticSnapshot, markdown => renderReportMarkdown(markdown)) : exportCapture.api
  if (staticSnapshot) {
    shell.style.maxWidth = 'none'
    const notice = document.createElement('div')
    notice.style.cssText = 'display:flex;flex-wrap:wrap;align-items:center;gap:12px;padding:10px 16px;border-bottom:1px solid hsl(var(--border));font:12px system-ui'
    const date = document.createElement('span')
    date.textContent = 'Updated ' + new Date(staticSnapshot.as_of).toLocaleString() + ' · Published snapshot'
    notice.append(date)
    if (/^https?:\/\//.test(staticSnapshot.live_url)) {
      const link = document.createElement('a')
      link.textContent = 'Open live dashboard'
      link.href = staticSnapshot.live_url
      link.target = '_blank'; link.rel = 'noopener noreferrer'; link.style.color = 'hsl(var(--primary))'
      notice.append(link)
    }
    shell.before(notice)
    document.title = 'Published dashboard'
  }
  frame.addEventListener('load', () => {
    if (!frame) return
    installReportHost(frame, {
      title: 'Dashboard preview',
      dataApi,
      tokenSource: document.documentElement,
      theme: currentTheme,
      dispatchData: true,
    })
    const doc = frame.contentDocument
    if (doc?.documentElement) {
      try {
        new MutationObserver(() => { mirrorLifecycle(); resizeFrame() })
          .observe(doc.documentElement, { attributes: true, attributeFilter: ['data-report-state'], childList: true, subtree: true })
        new ResizeObserver(resizeFrame).observe(doc.documentElement)
      } catch { /* observers unavailable */ }
    }
    if (staticSnapshot && doc) doc.addEventListener('click', event => {
      const control = (event.target as Element | null)?.closest('[data-report-action]')
      if (!control) return
      event.preventDefault(); event.stopImmediatePropagation()
      if (staticSnapshot.live_url) window.open(staticSnapshot.live_url, '_blank', 'noopener,noreferrer')
    }, true)
    mirrorLifecycle()
    resizeFrame()
  })
  // Set the authored document before insertion. Otherwise the initial blank
  // frame can fire load and report ready before this document has loaded.
  frame.srcdoc = withReportBootstrap(report.text)
  shell.appendChild(frame)
}

void start()
