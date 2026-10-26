import { ReportDocumentBridge } from './reportDocumentBridge'
import { REPORT_THEME_VARS, withReportBootstrap, type ReportHostState, type ReportHostTheme } from './reportHostRuntime'
import type { ReportDataApi } from './reportEmbedContext'
import type { ReportContentMenu } from './reportContentQuestion'
import type { ReportTabSelection } from './reportTabSelection'
import { selectReportDocument } from '../reportDocuments'

export type ReportDocumentContext = {
  title: string; kind: 'report' | 'html'; dataAvailable: boolean; dataMethods: string[]; workspacePath: string
  theme: ReportHostTheme; tokens: Record<string, string>; viewportHeight: number
  chatAvailable: boolean; chatVisible: boolean; functionsAvailable: boolean; blockedFunctions: string[]
  contentMenu: boolean; focus?: { value: string; token: number }; selection?: ReportTabSelection
}
export type ReportDocumentSnapshot = ReportHostState & { fitViewport?: boolean; renderer?: string; selection?: ReportTabSelection }
export type ReportFrameOptions = {
  runtimeUrl?: string; title: string; kind?: 'report' | 'html'; dataApi: ReportDataApi | null
  theme: ReportHostTheme; tokenSource?: HTMLElement | null
  widgets?: () => Partial<Pick<ReportDocumentContext, 'chatAvailable' | 'chatVisible' | 'functionsAvailable' | 'blockedFunctions'>>
  focus?: { value: string; token: number }; selection?: ReportTabSelection
  onState?: (state: ReportDocumentSnapshot) => void
  onContentMenu?: (menu: ReportContentMenu) => void
  onLink?: (href: string) => boolean
  onShortcut?: (event: KeyboardEvent) => boolean
}
// Keep the encoded document below Chromium's practical navigation URL limit.
const MAX_DOCUMENT_BYTES = 1_500_000
const DOCUMENT_TOO_LARGE = 'This page is too large to open (1.5 MB limit). Ask its designer to load large data separately, then publish it again.'
const methods = ['query', 'get', 'getText', 'getHtml', 'fileUrl', 'mediaUrl', 'openFile', 'updateField', 'updateFields', 'run', 'sendChatMessage', 'uploadFile', 'getCosts', 'getRelayRuns'] as const

/** Authenticated services stay here; the authored document receives values and named operations only. */
export class OpaqueReportFrame {
  readonly channel = crypto.randomUUID()
  readonly bridge: ReportDocumentBridge
  state: ReportDocumentSnapshot = { state: 'unloaded', errors: [], title: '', tabs: [], loadingTexts: [], markdownTexts: [], height: 0 }

  private tooLarge = false
  readonly frame: HTMLIFrameElement
  private options: () => ReportFrameOptions
  constructor(frame: HTMLIFrameElement, options: () => ReportFrameOptions) {
    this.frame = frame; this.options = options

    this.bridge = new ReportDocumentBridge(() => frame.contentWindow, this.channel)
    frame.setAttribute('sandbox', 'allow-scripts')
    frame.referrerPolicy = 'no-referrer'
    this.bridge.handle('initialize', () => this.context())
    for (const method of methods) this.bridge.handle('data.' + method, (...args) => {
      const api = this.options().dataApi
      const operation = api?.[method]
      if (typeof operation !== 'function') throw new Error('This operation is unavailable in this dashboard.')
      if (method === 'fileUrl') return Promise.resolve(Reflect.apply(operation, api, args)).then(async url => {
        if (typeof url !== 'string' || !url) return null
        const response = await fetch(url)
        if (!response.ok) throw new Error('The dashboard file could not be loaded.')
        // The child creates its own opaque-origin URL. Neither a preview URL's
        // credential nor an app-origin HTML blob URL crosses this boundary.
        return response.blob()
      })
      return Reflect.apply(operation, api, args)
    })
    this.bridge.handle('state', value => {
      if (!value || typeof value !== 'object') return
      this.state = value as ReportDocumentSnapshot
      this.options().onState?.(this.state)
    })
    this.bridge.handle('link.open', value => {
      if (typeof value !== 'string' || !value) return
      try { if (!['http:', 'https:', 'mailto:'].includes(new URL(value, window.location.href).protocol)) return } catch { return }
      const options = this.options()
      if (options.onLink?.(value)) return
      if (/^(https?:\/\/|mailto:)/i.test(value)) { window.open(value, '_blank', 'noopener,noreferrer'); return }
      const path = value.split(/[?#]/)[0].replace(/^\.\//, '')
      if (options.dataApi && /\.html$/i.test(path) && !path.includes('\\') && !path.split('/').includes('..') && !/^[a-z][a-z0-9+.-]*:/i.test(path)) {
        selectReportDocument(options.dataApi.workspacePath, path.startsWith('db/reports/') ? path : 'db/reports/' + path)
      }
    })
    this.bridge.handle('shortcut', value => {
      if (!value || typeof value !== 'object') return
      const key = value as KeyboardEvent
      if (typeof key.key !== 'string') return
      return this.options().onShortcut?.(new KeyboardEvent('keydown', { key: key.key, ctrlKey: Boolean(key.ctrlKey), metaKey: Boolean(key.metaKey), altKey: Boolean(key.altKey), shiftKey: Boolean(key.shiftKey), bubbles: true }))
    })
    this.bridge.handle('content.menu', value => {
      if (!value || typeof value !== 'object') return
      const menu = value as ReportContentMenu
      if (!menu.context || typeof menu.context.text !== 'string' || !Number.isFinite(menu.x) || !Number.isFinite(menu.y)) return
      const rect = frame.getBoundingClientRect()
      const sx = rect.width / (frame.clientWidth || rect.width || 1), sy = rect.height / (frame.clientHeight || rect.height || 1)
      this.options().onContentMenu?.({ x: rect.left + Math.max(0, Math.min(frame.clientWidth, menu.x)) * sx, y: rect.top + Math.max(0, Math.min(frame.clientHeight, menu.y)) * sy,
        context: { text: menu.context.text.slice(0, 2000), nearbyText: String(menu.context.nearbyText || '').slice(0, 2000), selected: Boolean(menu.context.selected), capturedAt: String(menu.context.capturedAt || '').slice(0, 40),
          ...(typeof menu.context.column === 'string' ? { column: menu.context.column.slice(0, 2000) } : {}), ...(typeof menu.context.element === 'string' ? { element: menu.context.element.slice(0, 200) } : {}) } })
    })
  }

  private context(): ReportDocumentContext {
    const options = this.options(), css = getComputedStyle(options.tokenSource ?? this.frame)
    const tokens: Record<string, string> = {}
    for (const name of REPORT_THEME_VARS) tokens[name] = css.getPropertyValue('--' + name).trim()
    const viewport = this.frame.closest('[data-report-viewport]')?.getBoundingClientRect()
    return { title: options.title, kind: options.kind ?? 'report', dataAvailable: Boolean(options.dataApi), dataMethods: methods.filter(name => typeof options.dataApi?.[name] === 'function'), workspacePath: options.dataApi?.workspacePath ?? '', theme: options.theme, tokens,
      viewportHeight: Math.max(0, Math.min(window.innerHeight, viewport?.bottom ?? window.innerHeight) - Math.max(this.frame.getBoundingClientRect().top, viewport?.top ?? 0)),
      chatAvailable: false, chatVisible: false, functionsAvailable: false, blockedFunctions: [], ...options.widgets?.(),
      contentMenu: Boolean(options.onContentMenu), focus: options.focus, selection: options.selection,
      ...(this.tooLarge ? { kind: 'html' as const, dataAvailable: false, dataMethods: [], chatAvailable: false, chatVisible: false, functionsAvailable: false, blockedFunctions: [], contentMenu: false } : {}) }
  }

  load(html: string) {
    const options = this.options()
    this.tooLarge = new TextEncoder().encode(html).length > MAX_DOCUMENT_BYTES
    const context = this.context()
    if (this.tooLarge) html = `<html data-report-state="error"><head><style>body{margin:0;padding:20px;font:14px/1.6 system-ui;color:hsl(var(--foreground));background:hsl(var(--background))}</style></head><body><p role="alert" data-report-error>${DOCUMENT_TOO_LARGE}</p></body></html>`
    const config = JSON.stringify({ channel: this.channel, ...context }).replace(/</g, '\\u003c')
    // Website frames load the asset published in the frontend's static root.
    // The dedicated preview supplies its own runtime URL when it creates a frame.
    const source = new URL(options.runtimeUrl ?? '/report-preview.js', window.location.href)
    source.search = ''; source.hash = ''
    const origin = new URL(options.runtimeUrl ? '.' : '/', source).href
    const head = `<base href="${origin}"><meta name="referrer" content="no-referrer"><script id="aw-native-document-config" type="application/json">${config}</script><script defer src="${source}"></script>`
    let document = context.kind === 'html' ? html : withReportBootstrap(html)
    const match = document.match(/<head[^>]*>/i)
    if (match?.index !== undefined) { const end = match.index + match[0].length; document = document.slice(0, end) + head + document.slice(end) }
    else document = head + document
    // Unlike an app-origin Blob, this URL stays opaque if someone opens the
    // document outside its sandbox, even while authored resources are loading.
    const bytes = new TextEncoder().encode(document)
    let binary = ''
    for (let offset = 0; offset < bytes.length; offset += 32768) binary += String.fromCharCode(...bytes.subarray(offset, offset + 32768))
    this.frame.src = 'data:text/html;charset=utf-8;base64,' + btoa(binary)
  }

  update(dispatchData = false) { this.bridge.notify('context', [this.context(), dispatchData]) }
  request(method: string, args: unknown[] = []) { return this.bridge.request(method, args) }
  dispose() {
    this.bridge.dispose()
  }
}
