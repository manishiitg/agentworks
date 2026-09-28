import { memo, useCallback, useEffect, useLayoutEffect, useRef } from 'react'
import { useReportDataApi } from './reportEmbedContext'
import { readReportTabSelection, restoreReportTabSelection, type ReportTabSelection } from './reportTabSelection'
import {
  applyReportTheme,
  installReportHost,
  withReportBootstrap,
  type ReportHostTheme,
} from './reportHostRuntime'

// Kept behind Vite's development flag: this lets us distinguish an iframe
// navigation from a normal React render when diagnosing report flicker, without
// adding production console noise.
function debugReportFrame(event: string, title: string, detail?: Record<string, unknown>) {
  if (!import.meta.env.DEV) return
  console.debug('[ReportFrame]', event, { title, ...detail })
}

// FORWARDED_APP_SHORTCUT_KEYS are the parent app's own global chords (App.tsx's
// window keydown handler): mode switches 1/2/3, workspace 6, auto-scroll 7,
// K quick switcher, N new chat. Anything outside this set stays with the
// browser so normal in-report keys keep working.
//
// Deliberately an allowlist: forwarding every ctrl/meta chord would hijack
// the browser's own in-frame behaviour (Ctrl+C copy, Ctrl+F find, Ctrl+A,
// Ctrl+P print), which must keep working while reading a report.
const FORWARDED_APP_SHORTCUT_KEYS = new Set(['1', '2', '3', '6', '7', 'k', 'n'])

function forwardAppShortcut(key: KeyboardEvent): boolean {
  if (!(key.ctrlKey || key.metaKey) || key.altKey) return false
  if (!FORWARDED_APP_SHORTCUT_KEYS.has(key.key.toLowerCase())) return false
  window.dispatchEvent(new KeyboardEvent('keydown', {
    key: key.key,
    ctrlKey: key.ctrlKey,
    metaKey: key.metaKey,
    shiftKey: key.shiftKey,
    altKey: key.altKey,
    bubbles: true,
  }))
  return true
}

function currentAppTheme(): ReportHostTheme {
  const cl = document.documentElement.classList
  return cl.contains('dark') || cl.contains('dark-plus') ? 'dark' : 'light'
}

// HtmlReportFrame renders an HTML report in a sandboxed iframe and installs the
// shared host runtime (reportHostRuntime.ts) on it: `window.report` (live data),
// theme mirroring, error surface, markdown link/image handling. The HTML owns
// ALL rendering (its own charts/tables/branded CSS) — we only deliver data —
// which is the right model for HTML: full styling freedom, no React-in-iframe
// or theme-matching. Re-injects + re-fires when the report's data changes so
// the HTML can re-render without a reload. Outside a report (no data API in
// context) the HTML renders standalone.
//
// Inside the HTML report:
//   await window.report.query(sql)   // read-only SQL against db/db.sqlite -> array of row objects
//   await window.report.get(path)    // any db/ knowledgebase/ docs file -> parsed JSON (or text)
//   await window.report.getText(path)// raw file text
//   await window.report.getHtml(path) // a markdown file rendered to an HTML string (wrapped in
//                                      // .report-markdown, default prose style injected) — drop a
//                                      // rendered .md inline: el.innerHTML = await window.report.getHtml(p)
//   await window.report.fileUrl(path)// blob URL for <img>/<a>/<iframe> (images, PDFs, …)
//   window.report.openFile(path)     // open a file in the in-report preview modal
//   await window.report.updateField(table, row_id, column, value) // write one cell;
//                                      // table/column validated against the live schema
//                                      // server-side, row matched on its own primary key —
//                                      // no SQL passes through this call. Rejects columns
//                                      // that identify or timestamp the row. Resolves
//                                      // { oldValue, newValue } once committed.
//   await window.report.updateFields(table, row_id, {col1: v1, col2: v2}) // form-style:
//                                      // write several columns on one row atomically (all
//                                      // fields applied, or none). Same validation as
//                                      // updateField, per column. Resolves
//                                      // { oldValues, newValues } keyed by column name.
//   await window.report.run('code/reports/x.py', {days: 7}) // LIVE data: runs a script in
//                                      // the workflow's code/ on the server (workflow's MCP
//                                      // servers + secrets, read-only DB snapshot, 60s cap)
//                                      // and resolves the one JSON value it printed. Args
//                                      // arrive as JSON in $REPORT_ARGS. Every call runs the
//                                      // script; it caches for itself in $REPORT_CACHE_DIR.
//   window.report.theme              // 'dark' | 'light' — the APP's current theme
//   window.addEventListener('report:data', render)   // fires on load + on data refresh
//   window.addEventListener('report:theme', restyle) // fires when the app theme toggles
//
// Theme: the iframe is a separate document and `@media (prefers-color-scheme)`
// only sees the OS, not the app's in-app light/dark toggle. So the runtime
// mirrors the app theme onto the iframe's <html> as BOTH a `.dark` class and a
// `data-theme="dark|light"` attribute, exposes `window.report.theme`, and keeps
// them in sync via a MutationObserver when the user toggles. Author HTML to key
// off `:root.dark` / `[data-theme="dark"]` (and prefers-color-scheme as a
// standalone fallback).
//
// autoHeight: size the iframe to its content (no inner scrollbar / clipping) and
// keep it in sync via a ResizeObserver as content renders. The outer report
// pane owns scrolling, so this frame must not impose its own scroll boundary.
function HtmlReportFrameComponent({
  html,
  title,
  className,
  autoHeight = false,
  refreshToken = 0,
  focusTarget,
}: {
  html: string
  title: string
  className: string
  autoHeight?: boolean
  // A report's live data is deliberately refreshed only when its owner asks.
  // Background workflow/status polling must never turn into an iframe reload.
  refreshToken?: number
  /** A top-level tab (or section) the report HTML should switch to. Delivered
   * as `report.focus` + a `report:focus` event; a report that does not listen
   * simply stays where it is. */
  focusTarget?: { value: string; token: number }
}) {
  const dataApi = useReportDataApi()
  const iframeRef = useRef<HTMLIFrameElement>(null)
  const observerRef = useRef<ResizeObserver | null>(null)
  const appliedHtmlRef = useRef<string | null>(null)
  const loadedDocumentRef = useRef<Document | null>(null)
  const injectedDocumentRef = useRef<Document | null>(null)
  const injectedDataApiRef = useRef<typeof dataApi>(null)
  const injectedRefreshTokenRef = useRef<number | null>(null)
  const appliedThemeRef = useRef<{ document: Document; theme: ReportHostTheme } | null>(null)
  const savedTabRef = useRef<ReportTabSelection | null>(null)

  // Do not pass srcDoc through React's normal DOM-prop reconciliation. A report
  // frame is a live document: when an outer polling update re-renders its
  // parent, Chromium can treat a reapplied srcDoc as a navigation and visibly
  // restart the report. Assign it imperatively only on initial mount or when
  // the report file's actual HTML changes.
  useLayoutEffect(() => {
    const frame = iframeRef.current
    if (!frame || appliedHtmlRef.current === html) return
    if (appliedHtmlRef.current !== null) savedTabRef.current = readReportTabSelection(frame.contentDocument)
    appliedHtmlRef.current = html
    injectedDocumentRef.current = null
    injectedDataApiRef.current = null
    injectedRefreshTokenRef.current = null
    appliedThemeRef.current = null
    loadedDocumentRef.current = null
    debugReportFrame('srcdoc assigned', title, { bytes: html.length })
    frame.srcdoc = withReportBootstrap(html)
  }, [html, title])

  // Mirror the APP's light/dark theme onto the iframe document (the agent's HTML
  // designs its own palette but keys the active mode off this). The app uses a
  // `.dark` (or `.dark-plus`) class on <html>.
  const applyTheme = useCallback((emitChange = true) => {
    const frame = iframeRef.current
    const doc = frame?.contentDocument
    if (!frame || !doc?.documentElement) return
    const theme = currentAppTheme()
    const previousTheme = appliedThemeRef.current?.document === doc
      ? appliedThemeRef.current.theme
      : null
    // Expose the app's resolved theme tokens (current light/dark + report theme)
    // as CSS variables inside the iframe so the HTML can use hsl(var(--…)).
    // The frame element itself is the token source so report-theme overrides
    // applied to the pane are included.
    applyReportTheme(frame, frame, theme, emitChange && previousTheme !== null && previousTheme !== theme)
    appliedThemeRef.current = { document: doc, theme }
  }, [])

  const resize = useCallback(() => {
    if (!autoHeight) return
    const frame = iframeRef.current
    const doc = frame?.contentDocument
    if (!frame || !doc || !doc.body) return
    // PLAT-160-adjacent. `documentElement.scrollHeight`/`body.scrollHeight` can
    // never report LESS than the iframe's own viewport height — the root
    // element fills the viewport by definition — so measuring while the frame
    // is already tall reads back the frame's own height and ratchets upward
    // forever. The previous fix collapsed the frame to 0px before measuring to
    // break that loop, but doing so changes the report's OWN viewport, and any
    // `vh`-sized content inside it — most concretely a report embedding its
    // own nested iframe sized with `min-height: calc(100vh - Npx)` — collapses
    // along with it. Measured live on salesoutreach's reporting dashboard: its
    // GTM-strategy tab's nested iframe genuinely renders at ~1884px, but
    // dropped to ~152px during the collapse window, so the outer frame was
    // sized to a small fraction of the real content with nothing left to
    // scroll to reach the rest — not a slow report, an invisible one.
    //
    // Measuring the bottom of each of body's direct children via
    // getBoundingClientRect instead avoids the viewport-floor problem
    // entirely — an element's own laid-out position reflects real content,
    // not the "root fills viewport" guarantee that requires collapsing
    // anything to sidestep. No frame-height mutation happens before the
    // measurement, so nothing inside the report ever sees an artificial
    // viewport change.
    const scrollY = doc.defaultView?.scrollY ?? 0
    let maxBottom = 0
    for (const child of Array.from(doc.body.children)) {
      const bottom = child.getBoundingClientRect().bottom + scrollY
      if (bottom > maxBottom) maxBottom = bottom
    }
    const content = Math.ceil(maxBottom)
    if (content <= 0) return
    const previousHeight = frame.style.height
    const nextHeight = `${content}px`
    if (previousHeight === nextHeight) return
    // The outer report pane owns scrolling. Let the frame reach its actual content
    // height so an HTML report never creates a second, nested scroll surface.
    debugReportFrame('height changed', frame.title, { from: previousHeight, to: nextHeight })
    frame.style.height = nextHeight
  }, [autoHeight])

  const inject = useCallback(() => {
    const frame = iframeRef.current
    const doc = frame?.contentDocument
    if (!frame || !doc) return
    const firstInjectionForDocument = injectedDocumentRef.current !== doc
    const dataApiChanged = injectedDataApiRef.current !== dataApi
    // A new iframe document and an explicit report refresh both need fresh
    // data. A normal parent render does not: assigning srcDoc again makes
    // Chromium navigate the iframe and visually "refresh" the report.
    const refreshRequested = injectedRefreshTokenRef.current !== refreshToken
    const dispatchData = firstInjectionForDocument || dataApiChanged || refreshRequested
    if (dispatchData) {
      debugReportFrame('report:data dispatched', title, { firstInjectionForDocument, dataApiChanged, refreshRequested, refreshToken })
    }

    installReportHost(frame, {
      title,
      dataApi,
      tokenSource: frame,
      theme: currentAppTheme(),
      dispatchData,
      forwardShortcut: forwardAppShortcut,
      debug: (event, detail) => debugReportFrame(event, title, detail),
    })
    appliedThemeRef.current = { document: doc, theme: currentAppTheme() }

    injectedDocumentRef.current = doc
    injectedDataApiRef.current = dataApi
    injectedRefreshTokenRef.current = refreshToken

    if (autoHeight) {
      observerRef.current?.disconnect()
      resize()
      try {
        const ro = new ResizeObserver(() => resize())
        if (doc.documentElement) ro.observe(doc.documentElement)
        if (doc.body) ro.observe(doc.body)
        observerRef.current = ro
      } catch {
        /* ResizeObserver unavailable — height stays at last measure */
      }
    }
  }, [dataApi, autoHeight, resize, refreshToken, title])

  // The ResizeObserver above watches elements inside the iframe's own
  // document. When an ancestor OUTSIDE the iframe (e.g. the workflow layout's
  // chat-focused single-pane mode on a narrow window, WorkflowLayout.tsx)
  // toggles display:none, Chromium suspends the iframe's own rendering
  // pipeline, and that internal observer does not reliably resume delivering
  // accurate notifications once it comes back — the outer scroll container
  // is then stuck at whatever height was last measured before the frame was
  // hidden, often 0. A window resize is dispatched on re-show for exactly
  // this (see WorkflowLayout.tsx's un-hide effect); listen for it here and
  // force a fresh measurement rather than hoping the observer catches up.
  useEffect(() => {
    if (!autoHeight) return
    window.addEventListener('resize', resize)
    return () => window.removeEventListener('resize', resize)
  }, [autoHeight, resize])

  // Re-inject when the report data changes (sources refreshed).
  useEffect(() => {
    // The first injection belongs exclusively to iframe onLoad. Calling it
    // before that can target the transient about:blank document and then make
    // the loaded report render a second time moments later.
    if (loadedDocumentRef.current !== iframeRef.current?.contentDocument) return
    inject()
  }, [inject])

  // A focus target from open_workspace_view(view="report", target="<tab>").
  // The report HTML owns its own tabs, so the platform cannot switch one: it
  // hands the name over as `report.focus` and fires `report:focus`, the same
  // shape as `report:theme`. A report that does not listen is unaffected.
  useEffect(() => {
    if (!focusTarget?.value) return
    const deliver = () => {
      // eslint-disable-next-line @typescript-eslint/no-explicit-any
      const win = iframeRef.current?.contentWindow as any
      if (!win?.report) return false
      try {
        win.report.focus = focusTarget.value
        win.dispatchEvent(new win.Event('report:focus'))
        return true
      } catch {
        return false // the frame navigated or reloaded under us
      }
    }
    if (deliver()) return
    // The report's own API is injected after load; retry briefly rather than
    // dropping a focus that arrived while the frame was still coming up.
    let tries = 0
    const timer = window.setInterval(() => {
      if (deliver() || ++tries > 20) window.clearInterval(timer)
    }, 100)
    return () => window.clearInterval(timer)
  }, [focusTarget?.value, focusTarget?.token])

  // Keep the iframe theme in sync when the user toggles the app's light/dark mode
  // while the report is open (watches the app's <html> class).
  useEffect(() => {
    const mo = new MutationObserver(() => applyTheme())
    mo.observe(document.documentElement, { attributes: true, attributeFilter: ['class'] })
    return () => mo.disconnect()
  }, [applyTheme])

  // Disconnect the observer on unmount.
  useEffect(() => () => observerRef.current?.disconnect(), [])

  return (
    <iframe
      ref={iframeRef}
      title={title}
      sandbox="allow-same-origin allow-scripts"
      onLoad={() => {
        loadedDocumentRef.current = iframeRef.current?.contentDocument || null
        debugReportFrame('iframe loaded', title)
        inject()
        const doc = iframeRef.current?.contentDocument
        const selection = savedTabRef.current
        savedTabRef.current = null
        if (doc && selection) restoreReportTabSelection(doc, selection)
      }}
      className={className}
    />
  )
}

// Report frames contain independently-running HTML. Keep their document stable
// while terminal, Pulse, and human-input polling updates the outer React tree.
// The owner passes refreshToken when a deliberate data refresh is wanted.
export const HtmlReportFrame = memo(HtmlReportFrameComponent)
