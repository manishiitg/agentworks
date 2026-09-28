// The iframe-side runtime of an HTML report: everything that turns a workflow's
// db/reports/index.html into a live page -- the pre-injection bootstrap stub,
// the window.report API, theme mirroring, link/image handling for rendered
// markdown, and the error surface. Plain DOM, no React, so the SAME code runs
// in the in-app Report tab (HtmlWidgetFrame) and in the headless
// preview_report page (src/report-preview). A report that passes preview
// therefore passes in the app: they render through one runtime.

import { getReportCosts, renderReportCosts } from './reportOperationalMetrics'
import type { ReportActivityOptions, ReportTableOptions } from './reportComposition'
import { renderReportActivity, renderReportTable } from './reportComposition'
import type { ReportCostOptions, ReportDataApi } from './reportEmbedContext'
import { getReportGoalMetrics, renderReportGoalProgress } from './reportGoalProgress'
import { REPORT_OPEN_ATTR, REPORT_SRC_ATTR } from './reportMarkdownLinks'
import { reportDaisyUiHead } from './reportDaisyUi'

export type ReportHostTheme = 'dark' | 'light'

// Lifecycle the host records on the report document's <html> element so an
// outside observer (the preview tool polling through the browser) can tell a
// still-loading page from a settled one without reading the page's own DOM.
export type ReportHostLifecycle = 'loading' | 'ready' | 'error'
export const REPORT_STATE_ATTR = 'data-report-state'

// The data API is injected at iframe onLoad — i.e. AFTER the report's own inline
// <script> has already parsed and run. So `window.report` does not exist while a
// report's top-level script executes, and every report had to invent its own way
// of waiting for it. They invented it badly and differently: hand-rolled
// `setInterval` polls that give up after N tries and run anyway, `DOMContentLoaded`
// handlers that double-fire alongside the poll, and `load().then(...)` chains with
// no `.catch()`. A report whose script then threw showed its "Loading…"
// placeholders forever — indistinguishable from still-working — because nothing
// surfaced the error. Measured across this workspace: 4 of 5 reports had no error
// handling at all.
//
// Prepending this stub makes `window.report.ready(fn)` available from the report's
// FIRST line, before any data exists. Callbacks queue until the real API is
// injected, then run — and re-run on every subsequent data refresh, so a report
// never needs to know about `report:data` or about injection timing at all. Errors
// thrown inside the callback (sync or async) are routed to the frame's error
// surface instead of vanishing.
//
// `.ready()` only helps a report that actually calls it. In practice most
// agent-authored reports reach for a more instinctive pattern instead —
// `DOMContentLoaded`, `window.onload`, or a bare top-level
// `(async()=>{ await window.report.query(...) })()` — all of which can run
// before injection, when `window.report.query` doesn't exist yet. Previously
// that was a synchronous TypeError (`.query is not a function`). So the stub
// also predefines `query`/`get`/`getText`/`getHtml`/`fileUrl`/`updateField`/`updateFields` itself: called
// before injection, each one queues its call and returns a pending promise
// instead of throwing; `inject()` below replays every queued call against the
// real API once it exists. This makes the wrong-but-instinctive pattern work
// correctly too, not just the documented one — the report doesn't need to know
// injection is asynchronous at all, regardless of which lifecycle hook it used.
//
// The theme is read from the host document right away (the sandbox allows
// same-origin, so the parent is reachable) and stamped onto <html> before the
// report's own CSS applies. A report reading window.report.theme at top level,
// or styling off :root.dark, used to see "light" for one paint even in dark
// mode.
//
// This is deliberately platform-owned rather than documented guidance: the report
// HTML is authored per workflow by an agent that never renders its own output, so
// a bootstrap it must write correctly every time is a bug generator. A bootstrap
// it inherits cannot be got wrong.
export const REPORT_BOOTSTRAP = `<script>(function(){
  if (window.report && window.report.ready) return;
  var queued = [];
  var pending = [];
  var api = window.report || {};
  api.ready = function(fn){
    if (typeof fn !== 'function') return;
    queued.push(fn);
    if (window.__reportApiReady) window.__runReportCallback(fn);
  };
  function queueCall(name){
    return function(){
      var args = Array.prototype.slice.call(arguments);
      return new Promise(function(resolve, reject){
        pending.push({ name: name, args: args, resolve: resolve, reject: reject });
      });
    };
  }
  ['query', 'get', 'getText', 'getHtml', 'fileUrl', 'mediaUrl', 'updateField', 'updateFields', 'run', 'getGoalMetrics', 'renderGoalProgress', 'getCosts', 'renderCosts', 'renderTable', 'renderActivity'].forEach(function(name){
    api[name] = queueCall(name);
  });
  api.openFile = function(){
    pending.push({ name: 'openFile', args: Array.prototype.slice.call(arguments), resolve: function(){}, reject: function(){} });
  };
  // Actions must not be replayed from a render/initialization callback.
  api.sendChatMessage = function(){
    return Promise.reject(new Error('Dashboard chat is not ready. Try again after the dashboard loads.'));
  };
  var theme = 'light';
  try {
    var hostRoot = window.parent && window.parent !== window ? window.parent.document.documentElement : null;
    if (hostRoot && (hostRoot.classList.contains('dark') || hostRoot.classList.contains('dark-plus') || hostRoot.getAttribute('data-theme') === 'dark')) theme = 'dark';
  } catch (e) {}
  api.theme = theme;
  try {
    var root = document.documentElement;
    if (theme === 'dark') root.classList.add('dark');
    root.setAttribute('data-theme', theme);
    root.setAttribute('${REPORT_STATE_ATTR}', 'loading');
  } catch (e) {}
  window.__reportQueuedCallbacks = queued;
  window.__reportPendingCalls = pending;
  window.__reportHostErrors = [];
  window.report = api;
})();</script>`

// Placed immediately after <head> when present so it precedes any author script;
// falls back to prepending for a fragment without a full document shell.
export function withReportBootstrap(html: string): string {
  if (html.includes('window.__reportQueuedCallbacks')) return html
  const platformStyles = reportDaisyUiHead(html)
  const bootstrap = platformStyles + REPORT_BOOTSTRAP
  const headOpen = html.match(/<head[^>]*>/i)
  if (headOpen?.index !== undefined) {
    const at = headOpen.index + headOpen[0].length
    return html.slice(0, at) + bootstrap + html.slice(at)
  }
  const htmlOpen = html.match(/<html[^>]*>/i)
  if (htmlOpen?.index !== undefined) {
    const at = htmlOpen.index + htmlOpen[0].length
    return html.slice(0, at) + bootstrap + html.slice(at)
  }
  return bootstrap + html
}

// App theme tokens (HSL triplets) exposed to the HTML report as CSS variables so
// it can match the app palette via hsl(var(--…)) and switch with light/dark. Read
// from the (themed) host element so report-theme overrides are included.
export const REPORT_THEME_VARS = [
  'background', 'foreground', 'card', 'card-foreground', 'popover', 'popover-foreground',
  'primary', 'primary-foreground', 'secondary', 'secondary-foreground',
  'muted', 'muted-foreground', 'accent', 'accent-foreground',
  'border', 'input', 'ring', 'destructive', 'destructive-foreground',
  'chart-1', 'chart-2', 'chart-3', 'chart-4', 'chart-5',
] as const

export function injectThemeTokens(host: HTMLElement, doc: Document) {
  const cs = getComputedStyle(host)
  const decls = REPORT_THEME_VARS
    .map((v) => {
      const val = cs.getPropertyValue(`--${v}`).trim()
      return val ? `--${v}:${val};` : ''
    })
    .join('')
  if (!decls) return
  let style = doc.getElementById('__report_theme_tokens') as HTMLStyleElement | null
  if (!style) {
    style = doc.createElement('style')
    style.id = '__report_theme_tokens'
    doc.head?.appendChild(style)
  }
  style.textContent = `:root{${decls}}`
}

// Drop the about:srcdoc UA default `body{margin:8px}` so an HTML report renders
// edge-to-edge inside the report pane (we strip all our own chrome for HTML — the
// 8px UA margin would otherwise read as a stray gap around every side). Inserted
// as the FIRST <head> child so the report's own CSS (later, equal specificity)
// still wins if it sets its own body margin/padding. Idempotent per document.
export function injectBaseReset(doc: Document) {
  if (doc.getElementById('__report_base_reset')) return
  const style = doc.createElement('style')
  style.id = '__report_base_reset'
  style.textContent = 'html,body{margin:0;padding:0;}'
  const head = doc.head
  if (head) head.insertBefore(style, head.firstChild)
}

// Default prose style for markdown injected via window.report.getHtml() — the
// helper wraps output in `.report-markdown`, and this gives it readable,
// theme-aware typography (using the app tokens injected by injectThemeTokens) so
// an embedded .md looks right with zero effort. The report can override any of
// these in its own CSS. Inserted after the theme-token style so var(--…) resolve.
export function injectMarkdownStyles(doc: Document) {
  if (doc.getElementById('__report_markdown_styles')) return
  const style = doc.createElement('style')
  style.id = '__report_markdown_styles'
  style.textContent = `
.report-markdown{color:hsl(var(--foreground,222 47% 11%));line-height:1.6;font-size:0.95rem}
.report-markdown h1,.report-markdown h2,.report-markdown h3,.report-markdown h4{line-height:1.25;font-weight:650;margin:1.4em 0 .5em}
.report-markdown h1{font-size:1.5em}.report-markdown h2{font-size:1.25em}.report-markdown h3{font-size:1.1em}
.report-markdown p,.report-markdown ul,.report-markdown ol{margin:.6em 0}
.report-markdown ul,.report-markdown ol{padding-left:1.4em}
.report-markdown a{color:hsl(var(--primary,222 89% 55%));text-decoration:underline;text-underline-offset:2px}
.report-markdown code{font-family:ui-monospace,SFMono-Regular,Menlo,monospace;font-size:.88em;background:hsl(var(--muted,210 40% 96%));padding:.12em .35em;border-radius:4px}
.report-markdown pre{background:hsl(var(--muted,210 40% 96%));padding:.8em 1em;border-radius:8px;overflow:auto}
.report-markdown pre code{background:none;padding:0}
.report-markdown blockquote{margin:.6em 0;padding:.2em .9em;border-left:3px solid hsl(var(--border,214 32% 88%));color:hsl(var(--muted-foreground,215 16% 47%))}
.report-markdown table{border-collapse:collapse;width:100%;margin:.7em 0;font-size:.9em}
.report-markdown th,.report-markdown td{border:1px solid hsl(var(--border,214 32% 88%));padding:.4em .6em;text-align:left}
.report-markdown th{background:hsl(var(--muted,210 40% 96%));font-weight:600}
.report-markdown img{max-width:100%;height:auto}
.report-markdown hr{border:0;border-top:1px solid hsl(var(--border,214 32% 88%));margin:1.2em 0}
`.trim()
  doc.head?.appendChild(style)
}

// eslint-disable-next-line @typescript-eslint/no-explicit-any
type AnyWindow = any
// eslint-disable-next-line @typescript-eslint/no-explicit-any
type AnyDocument = any

function reportWindow(frame: HTMLIFrameElement): AnyWindow {
  return frame.contentWindow as AnyWindow
}

function setLifecycle(doc: Document, state: ReportHostLifecycle) {
  try {
    doc.documentElement?.setAttribute(REPORT_STATE_ATTR, state)
  } catch {
    /* document may have navigated away */
  }
}

/** Mirror a theme onto the report document (class + data attribute, the
 * value on window.report, and the app palette tokens read from `tokenSource`).
 * Fires `report:theme` inside the frame only when asked, so an initial
 * application does not double-render a report that also renders on
 * `report:data`. */
export function applyReportTheme(
  frame: HTMLIFrameElement,
  tokenSource: HTMLElement | null,
  theme: ReportHostTheme,
  emitChange: boolean,
): void {
  const win = reportWindow(frame)
  const doc = frame.contentDocument
  if (!win || !doc?.documentElement) return
  doc.documentElement.classList.toggle('dark', theme === 'dark')
  doc.documentElement.setAttribute('data-theme', theme)
  if (win.report) win.report.theme = theme
  if (tokenSource) injectThemeTokens(tokenSource, doc)
  if (emitChange) {
    try {
      win.dispatchEvent(new win.Event('report:theme'))
    } catch {
      /* iframe may have navigated/reloaded */
    }
  }
}

export interface ReportHostInstallOptions {
  /** Used only for diagnostics. */
  title: string
  /** The live data API; null renders the HTML standalone with no window.report. */
  dataApi: ReportDataApi | null
  /** Element whose computed `--token` custom properties are the app palette
   * (the themed app root, or the frame itself in the app). */
  tokenSource: HTMLElement | null
  theme: ReportHostTheme
  /** Fire `report:data` and flush every queued ready() callback. True for a
   * new document, a changed data API, or an explicit refresh; false for a
   * re-injection that must not re-render (a parent React render). */
  dispatchData: boolean
  /** Optional: the app's own global shortcuts (mode switches, quick
   * switcher) are bound on the parent window and never see keydown inside
   * the frame. Return true to swallow the event after forwarding it. */
  forwardShortcut?: (event: KeyboardEvent) => boolean
  debug?: (event: string, detail?: Record<string, unknown>) => void
}

/**
 * Install (or re-install) the host runtime on a loaded report document.
 * Idempotent per document for the parts that bind listeners; the API object
 * is replaced on every call so a changed dataApi takes effect.
 */
export function installReportHost(frame: HTMLIFrameElement, options: ReportHostInstallOptions): void {
  const win = reportWindow(frame)
  const doc = frame.contentDocument as (Document & AnyDocument) | null
  if (!win || !doc) return
  const { title, dataApi, dispatchData } = options
  const debug = options.debug ?? (() => {})

  injectBaseReset(doc)
  injectMarkdownStyles(doc)

  if (options.forwardShortcut && !doc.__reportShortcutsBound) {
    doc.__reportShortcutsBound = true
    const forward = options.forwardShortcut
    doc.addEventListener('keydown', (e: Event) => {
      if (forward(e as KeyboardEvent)) e.preventDefault()
    })
  }

  // In a srcDoc iframe the base URL is about:srcdoc, so clicking an in-page
  // `#anchor` link (the report's tab nav) reloads the WHOLE document instead of
  // scrolling. Intercept those clicks and scroll manually. Bound once per loaded
  // document (the flag resets on reload, so a fresh doc re-binds).
  if (!doc.__anchorScrollBound) {
    doc.__anchorScrollBound = true
    doc.addEventListener('click', (e: Event) => {
      const el = e.target as Element | null
      const link = el?.closest?.('a[href]') as HTMLAnchorElement | null
      if (!link) return
      const href = link.getAttribute('href') || ''

      // A workspace file link inside rendered markdown (rewritten by
      // reportMarkdownLinks): open it in the in-report preview modal.
      const openPath = link.getAttribute(REPORT_OPEN_ATTR)
      if (openPath) {
        e.preventDefault()
        // Read the CURRENT api: the handler is bound once per document but
        // the data API may be replaced by a later re-injection.
        win.report?.openFile?.(openPath)
        return
      }

      if (href.startsWith('#')) {
        const target = doc.getElementById(href.slice(1))
        if (!target) return
        e.preventDefault()
        target.scrollIntoView({ behavior: 'smooth', block: 'start' })
        return
      }

      // External links (Notion, Jira, GitHub, docs, …): the iframe sandbox has
      // no allow-popups/allow-top-navigation, so a click would be silently
      // swallowed. Open them in a new browser tab from the parent window
      // instead, keeping the sandbox locked down.
      if (/^https?:\/\//i.test(href)) {
        e.preventDefault()
        window.open(href, '_blank', 'noopener,noreferrer')
      }
    })
  }

  // A report's own script failing used to be completely silent: nothing here
  // listened for `error` or `unhandledrejection`, so a throw inside the page's
  // render left its "Loading…" placeholders on screen forever with no clue why
  // — the report looked like it was still working, indefinitely. Reports are
  // agent-authored per workflow and validate_report_html is static: it cannot
  // catch a script that dies before it writes anything. Surfacing the error in
  // place of the spinner is done in the host rather than in each report: it
  // fixes every existing report at once and cannot be forgotten by whatever
  // authors the next one.
  const showReportError = (message: string, source?: string) => {
    const text = String(message || 'Unknown error').slice(0, 400)
    if (Array.isArray(win.__reportHostErrors)) {
      win.__reportHostErrors.push(source ? `${text} (${source})` : text)
    }
    setLifecycle(doc, 'error')
    if (doc.__reportErrorShown) return
    doc.__reportErrorShown = true
    debug('report script error', { title, message: text, source })
    const banner = doc.createElement('div')
    banner.setAttribute('data-report-error', '1')
    banner.style.cssText = [
      'margin:12px',
      'padding:12px 14px',
      'border:1px solid #ef4444',
      'border-left-width:4px',
      'border-radius:6px',
      'background:rgba(239,68,68,0.08)',
      'color:#b91c1c',
      'font:13px/1.5 ui-monospace,SFMono-Regular,Menlo,monospace',
      'white-space:pre-wrap',
      'word-break:break-word',
    ].join(';')
    banner.textContent =
      'This report failed to render.\n\n' +
      text +
      (source ? `\n\n(${source})` : '') +
      '\n\nData loaded through window.report is unavailable, so any "Loading…" ' +
      'text below is stale rather than in progress.'
    try {
      doc.body?.insertBefore(banner, doc.body.firstChild)
    } catch {
      /* document may have navigated away mid-error */
    }
  }
  if (!doc.__reportErrorBound) {
    doc.__reportErrorBound = true
    win.addEventListener('error', (e: Event) => {
      const err = e as ErrorEvent
      showReportError(
        err?.error?.stack || err?.message || 'Script error',
        err?.filename ? `${err.filename}:${err.lineno}` : undefined,
      )
    })
    win.addEventListener('unhandledrejection', (e: Event) => {
      const reason = (e as AnyWindow)?.reason
      showReportError(
        reason?.stack || reason?.message || String(reason ?? 'Unhandled promise rejection'),
        'unhandled promise rejection',
      )
    })
  }

  // Images inside rendered markdown (reportMarkdownLinks) carry the
  // workspace path on a data attribute instead of src, because a relative
  // src resolves to nothing under about:srcdoc. Load each through the
  // authenticated file channel, now and whenever the report inserts more
  // markdown later (a tab switch, a data refresh).
  if (dataApi && !doc.__reportImageResolverBound) {
    doc.__reportImageResolverBound = true
    const resolveImages = () => {
      const api = win.report as ReportDataApi | undefined
      if (!api?.fileUrl) return
      doc.querySelectorAll(`img[${REPORT_SRC_ATTR}]:not([src])`).forEach((node: Element) => {
        const img = node as HTMLImageElement
        const path = img.getAttribute(REPORT_SRC_ATTR)
        if (!path || img.dataset.reportSrcPending) return
        img.dataset.reportSrcPending = '1'
        void Promise.resolve(api.fileUrl(path)).then((url) => {
          if (url) img.src = url
          else img.alt = img.alt || `Missing file: ${path}`
        }).catch(() => { /* leave the alt text */ })
      })
    }
    resolveImages()
    try {
      new MutationObserver(resolveImages).observe(doc.documentElement, { childList: true, subtree: true })
    } catch {
      /* MutationObserver unavailable — only the initial pass runs */
    }
  }

  // Run one report.ready() callback with its errors surfaced rather than
  // swallowed. A callback may be sync or return a promise; both are covered,
  // which is the specific gap that made `load().then(...)` with no `.catch()`
  // fail silently. Returns the settled promise so the lifecycle can wait on it.
  const runReportCallback = (fn: unknown): Promise<void> => {
    if (typeof fn !== 'function') return Promise.resolve()
    try {
      const result = (fn as () => unknown)()
      const maybe = result as AnyWindow
      if (maybe && typeof maybe.then === 'function') {
        return Promise.resolve(maybe).then(
          () => undefined,
          (err: AnyWindow) => { showReportError(err?.stack || err?.message || String(err), 'report.ready()') },
        )
      }
    } catch (err) {
      const e = err as AnyWindow
      showReportError(e?.stack || e?.message || String(e), 'report.ready()')
    }
    return Promise.resolve()
  }
  win.__runReportCallback = runReportCallback

  if (!dataApi) return

  const getCosts = (costOptions?: ReportCostOptions) => getReportCosts(dataApi, costOptions)
  const renderCosts = (target: string | HTMLElement, costOptions?: ReportCostOptions) => renderReportCosts(doc, dataApi, target, costOptions)
  const getGoalMetrics = () => getReportGoalMetrics(dataApi.query)
  const renderGoalProgress = (target: string | HTMLElement) => renderReportGoalProgress(doc, dataApi.query, target)
  const renderTable = (target: string | HTMLElement, tableOptions: ReportTableOptions) => renderReportTable(doc, dataApi.query, target, tableOptions)
  const renderActivity = (target: string | HTMLElement, activityOptions?: ReportActivityOptions) => renderReportActivity(doc, dataApi, target, activityOptions)

  win.report = {
    getCosts, renderCosts,
    getGoalMetrics,
    renderGoalProgress,
    renderTable,
    renderActivity,
    ready: (fn: unknown) => {
      if (typeof fn !== 'function') return
      const queue = win.__reportQueuedCallbacks
      if (Array.isArray(queue) && !queue.includes(fn)) queue.push(fn)
      void runReportCallback(fn)
    },
    workspacePath: dataApi.workspacePath,
    query: dataApi.query,
    get: dataApi.get,
    getText: dataApi.getText,
    getHtml: dataApi.getHtml,
    renderMarkdown: dataApi.renderMarkdown,
    fileUrl: dataApi.fileUrl,
    mediaUrl: dataApi.mediaUrl,
    openFile: dataApi.openFile,
    updateField: dataApi.updateField,
    updateFields: dataApi.updateFields,
    run: dataApi.run,
    sendChatMessage: dataApi.sendChatMessage,
    theme: options.theme,
  }

  // The bootstrap's stub queued any query/get/getText/getHtml/fileUrl/openFile/
  // updateField/updateFields call made before this injection (DOMContentLoaded,
  // window.onload, a bare top-level await — anything that ran before window.report
  // was the real API). Replay each against the real methods now instead of leaving
  // those promises pending forever. Runs once per document: the bootstrap's pending
  // array is drained and cleared here, so it stays empty on every later
  // re-injection (data refresh, theme change).
  const realReportMethods: Record<string, ((...args: unknown[]) => unknown) | undefined> = {
    getCosts: getCosts as (...args: unknown[]) => unknown,
    renderCosts: renderCosts as (...args: unknown[]) => unknown,
    getGoalMetrics,
    renderGoalProgress: renderGoalProgress as (...args: unknown[]) => unknown,
    renderTable: renderTable as (...args: unknown[]) => unknown,
    renderActivity: renderActivity as (...args: unknown[]) => unknown,
    query: dataApi.query as (...args: unknown[]) => unknown,
    get: dataApi.get as (...args: unknown[]) => unknown,
    getText: dataApi.getText as (...args: unknown[]) => unknown,
    getHtml: dataApi.getHtml as (...args: unknown[]) => unknown,
    fileUrl: dataApi.fileUrl as (...args: unknown[]) => unknown,
    mediaUrl: dataApi.mediaUrl as (...args: unknown[]) => unknown,
    updateField: dataApi.updateField as (...args: unknown[]) => unknown,
    updateFields: dataApi.updateFields as (...args: unknown[]) => unknown,
    run: dataApi.run as (...args: unknown[]) => unknown,
  }
  type QueuedReportCall = {
    name: string
    args: unknown[]
    resolve: (value: unknown) => void
    reject: (reason: unknown) => void
  }
  const pendingCalls: QueuedReportCall[] = Array.isArray(win.__reportPendingCalls)
    ? [...win.__reportPendingCalls]
    : []
  win.__reportPendingCalls = []
  const replayed: Promise<unknown>[] = []
  pendingCalls.forEach(({ name, args, resolve, reject }) => {
    if (name === 'openFile') {
      try {
        dataApi.openFile(...(args as Parameters<typeof dataApi.openFile>))
      } catch {
        /* best-effort — openFile has no result to resolve */
      }
      return
    }
    const fn = realReportMethods[name]
    if (typeof fn !== 'function') {
      reject(new Error(`window.report.${name} is unavailable`))
      return
    }
    const settled = Promise.resolve()
      .then(() => fn(...args))
      .then(resolve, reject)
    replayed.push(settled)
  })

  // Initial theme application is setup, not a theme change. The single
  // report:data event below owns the initial render. This avoids every HTML
  // report doing a full data render once for theme and again for data.
  applyReportTheme(frame, options.tokenSource, options.theme, false)

  if (!dispatchData) return

  debug('report:data dispatched', { title })
  // Mark the API live BEFORE flushing, so a ready() registered from inside
  // a callback runs immediately rather than waiting for the next refresh.
  win.__reportApiReady = true
  try {
    win.dispatchEvent(new win.Event('report:data'))
  } catch {
    /* iframe may have navigated/reloaded */
  }
  // Run everything the page queued via report.ready() before the API
  // existed, and re-run it on each subsequent refresh — the same contract
  // as the report:data listener, but without the page having to know that
  // event exists or that injection is asynchronous. Iterated over a copy:
  // a callback may register another ready() while running.
  const queued = Array.isArray(win.__reportQueuedCallbacks)
    ? [...win.__reportQueuedCallbacks]
    : []
  if (queued.length > 0) debug('report.ready callbacks flushed', { title, count: queued.length })
  const settled = queued.map(runReportCallback)
  // "Ready" means every ready() callback and every replayed early call has
  // settled -- the point at which a report has either drawn its data or
  // failed. A report that listens to report:data directly settles at
  // dispatch. Errors leave the state at 'error'.
  void Promise.allSettled([...settled, ...replayed]).then(() => {
    if (doc.documentElement?.getAttribute(REPORT_STATE_ATTR) !== 'error') setLifecycle(doc, 'ready')
  })
}

export interface ReportHostState {
  state: ReportHostLifecycle | 'unloaded'
  errors: string[]
  title: string
  /** Texts of elements that look like top-level navigation (role=tab,
   * [data-tab], nav buttons) -- what a reviewer would call "the tabs". */
  tabs: string[]
  /** Visible text still saying "Loading…" after the page settled: stale
   * placeholders a render never replaced. */
  loadingTexts: string[]
  /** Visible text that looks like unrendered markdown (bold markers, ATX
   * headings, link syntax): agent-written markdown escaped as plaintext
   * instead of rendered. Excerpts, truncated. */
  markdownTexts: string[]
  height: number
}

/** What an outside observer can learn about a loaded report document. */
export function readReportHostState(frame: HTMLIFrameElement | null): ReportHostState {
  const doc = frame?.contentDocument
  const win = frame ? reportWindow(frame) : null
  if (!doc?.documentElement) {
    return { state: 'unloaded', errors: [], title: '', tabs: [], loadingTexts: [], markdownTexts: [], height: 0 }
  }
  const state = (doc.documentElement.getAttribute(REPORT_STATE_ATTR) as ReportHostLifecycle | null) ?? 'loading'
  const errors: string[] = Array.isArray(win?.__reportHostErrors) ? [...win.__reportHostErrors] : []
  const tabs = Array.from(doc.querySelectorAll('[role="tab"], [data-tab], nav button, nav a, .tabs button, .tab-bar button'))
    .map((el) => (el.textContent || '').replace(/\s+/g, ' ').trim())
    .filter((text, index, all) => text && text.length <= 40 && all.indexOf(text) === index)
    .slice(0, 24)
  const loadingTexts = Array.from(doc.body?.querySelectorAll('*') ?? [])
    .filter((el) => el.children.length === 0)
    .map((el) => (el.textContent || '').trim())
    .filter((text) => /^(loading|loading…|loading\.\.\.|fetching)\b/i.test(text))
    .filter((text, index, all) => all.indexOf(text) === index)
    .slice(0, 12)
  const markdownPatterns = [
    /\*\*[^*\n]+\*\*/,
    /__[^_\n]+__/,
    /(^|\n)#{1,6}\s+\S/,
    /\[[^\]\n]+\]\(https?:/,
    /!\[[^\]\n]*\]\(/,
    /(^|\n)```\S/,
  ]
  const excerpt = (text: string) => {
    const flat = text.replace(/\s+/g, ' ').trim()
    if (flat.length <= 120) return flat
    let at = -1
    // Flat-text variants: newlines are gone, so line anchors are dropped.
    for (const pattern of [/\*\*[^*]+\*\*/, /__[^_]+__/, /#{1,6}\s+\S/, /\[[^\]]+\]\(https?:/, /!\[[^\]]*\]\(/, /```\S/]) {
      const found = flat.search(pattern)
      if (found >= 0 && (at < 0 || found < at)) at = found
    }
    if (at < 0) return flat.slice(0, 117) + '…'
    const start = Math.max(0, at - 40)
    const slice = flat.slice(start, start + 117)
    return (start > 0 ? '…' : '') + slice + (start + 117 < flat.length ? '…' : '')
  }
  const markdownTexts = Array.from(doc.body?.querySelectorAll('*') ?? [])
    .filter((el) => el.children.length === 0)
    .map((el) => (el.textContent || '').trim())
    .filter((text) => text && markdownPatterns.some((pattern) => pattern.test(text)))
    .map(excerpt)
    .filter((text, index, all) => all.indexOf(text) === index)
    .slice(0, 12)
  let height = 0
  const scrollY = doc.defaultView?.scrollY ?? 0
  for (const child of Array.from(doc.body?.children ?? [])) {
    const bottom = child.getBoundingClientRect().bottom + scrollY
    if (bottom > height) height = bottom
  }
  return { state, errors, title: doc.title || '', tabs, loadingTexts, markdownTexts, height: Math.ceil(height) }
}
