import { ReportDocumentBridge } from './reportDocumentBridge'
import { applyReportDocumentTheme, installReportDocumentHost, readReportDocumentState, REPORT_THEME_VARS } from './reportHostRuntime'
import { renderReportMarkdown } from './reportMarkdown'
import { installDashboardChatWidgets } from './dashboardChatWidgets'
import { installDashboardActionButtons } from './reportActionButtons'
import { bindReportContentMenu } from './reportContentQuestion'
import { readReportTabSelection, restoreReportTabSelection } from './reportTabSelection'
import { reportMotionCapture } from './reportMotion'
import { inspectReportUI } from '../../../report-preview/uiChecks'
import type { ReportDataApi } from './reportEmbedContext'
import type { ReportDocumentContext } from './opaqueReportFrame'
import { REPORT_OPEN_ATTR } from './reportMarkdownLinks'

/** Runs inside the opaque document. No app storage, credentials or parent DOM are available. */
export function startReportFrameDocument(initial: ReportDocumentContext & { channel: string }) {
  const bridge = new ReportDocumentBridge(() => window.parent, initial.channel)
  let context = initial as ReportDocumentContext
  let chat: ReturnType<typeof installDashboardChatWidgets> | undefined
  let actions: ReturnType<typeof installDashboardActionButtons> | undefined
  let removeMenu: (() => void) | undefined
  let focusToken: number | undefined
  let selectionRestored = false
  let animation = 0
  const call = (name: string, args: unknown[]) => bridge.request('data.' + name, args)
  const api: ReportDataApi = {
    workspacePath: initial.workspacePath,
    query: (sql, params) => call('query', [sql, params]) as ReturnType<ReportDataApi['query']>,
    get: path => call('get', [path]),
    getText: path => call('getText', [path]) as ReturnType<ReportDataApi['getText']>,
    getHtml: path => call('getHtml', [path]) as ReturnType<ReportDataApi['getHtml']>,
    renderMarkdown: renderReportMarkdown,
    fileUrl: async path => { const blob = await call('fileUrl', [path]); return blob instanceof Blob ? URL.createObjectURL(blob) : null },
    mediaUrl: path => call('mediaUrl', [path]) as ReturnType<ReportDataApi['mediaUrl']>,
    openFile: path => { void call('openFile', [path]) },
    updateField: (...args) => call('updateField', args) as ReturnType<ReportDataApi['updateField']>,
    updateFields: (...args) => call('updateFields', args) as ReturnType<ReportDataApi['updateFields']>,
    run: (...args) => call('run', args),
    sendChatMessage: (...args) => call('sendChatMessage', args) as ReturnType<ReportDataApi['sendChatMessage']>,
    uploadFile: file => call('uploadFile', [file]) as ReturnType<NonNullable<ReportDataApi['uploadFile']>>,
  }
  const snapshot = () => ({ ...readReportDocumentState(document), renderer: document.documentElement.dataset.reportRenderer, fitViewport: document.documentElement.getAttribute('data-report-fit') === 'viewport', selection: readReportTabSelection(document) ?? undefined })
  const publishState = () => {
    if (animation) return
    animation = requestAnimationFrame(() => {
      animation = 0
      if (document.documentElement.getAttribute('data-report-state') === 'ready') {
        if (!selectionRestored && context.selection) selectionRestored = restoreReportTabSelection(document, context.selection)
        if (context.focus && context.focus.token !== focusToken) {
          focusToken = context.focus.token
          const report = (window as unknown as { report?: { focus?: string } }).report
          if (report) { report.focus = context.focus.value; window.dispatchEvent(new Event('report:focus')) }
        }
      }
      bridge.notify('state', [snapshot()])
    })
  }
  const apply = (next: ReportDocumentContext, dispatchData: boolean) => {
    const themeChanged = context.theme !== next.theme
    const widgetsChanged = context.chatAvailable !== next.chatAvailable || context.chatVisible !== next.chatVisible || context.functionsAvailable !== next.functionsAvailable
    if (widgetsChanged) { chat?.dispose(); chat = undefined; actions?.dispose(); actions = undefined }
    context = next
    api.workspacePath = next.workspacePath
    api.getCosts = next.dataMethods.includes('getCosts') ? options => call('getCosts', [options]) as ReturnType<NonNullable<ReportDataApi['getCosts']>> : undefined
    api.getRelayRuns = next.dataMethods.includes('getRelayRuns') ? options => call('getRelayRuns', [options]) as ReturnType<NonNullable<ReportDataApi['getRelayRuns']>> : undefined
    for (const name of REPORT_THEME_VARS) document.documentElement.style.setProperty('--' + name, next.tokens[name] || '')
    if (next.kind === 'report') {
      if (!chat) chat = installDashboardChatWidgets(document, {
        available: next.chatAvailable, visible: next.chatVisible, viewportHeight: () => context.viewportHeight,
        openConversation: async () => { await bridge.request('chat.open') },
        changed: (slots, authored) => bridge.notify('widgets.layout', [slots, authored]),
        popup: open => bridge.notify('chat.popup', [open]), error: message => bridge.notify('chat.error', [message]),
        themeChanged: theme => bridge.notify('widgets.theme', [theme]),
      })
      if (!actions) actions = installDashboardActionButtons(document, { available: next.functionsAvailable, open: request => { void bridge.request('action.open', [request]) } })
      actions.setBlocked(next.blockedFunctions)
      installReportDocumentHost(document, {
        title: next.title, dataApi: next.dataAvailable ? api : null, theme: next.theme, tokenSource: null, dispatchData,
        chatApi: chat.api,
        forwardShortcut: key => {
          if (!(key.ctrlKey || key.metaKey) || key.altKey || !['1', '2', '3', '6', '7', 'k', 'n'].includes(key.key.toLowerCase())) return false
          bridge.notify('shortcut', [{ key: key.key, ctrlKey: key.ctrlKey, metaKey: key.metaKey, altKey: key.altKey, shiftKey: key.shiftKey }]); return true
        },
      })
      removeMenu?.()
      removeMenu = bindReportContentMenu(document, menu => bridge.notify('content.menu', [menu]), () => context.contentMenu)

    }
    if (next.kind === 'html' && document.documentElement.getAttribute('data-report-state') !== 'error') document.documentElement.setAttribute('data-report-state', 'ready')
    applyReportDocumentTheme(document, null, next.theme, themeChanged && !dispatchData)
    window.dispatchEvent(new Event('resize'))
    publishState()
  }
  bridge.handle('context', (next, refresh) => {
    if (next && typeof next === 'object') apply(next as ReportDocumentContext, refresh === true)
  })
  bridge.handle('widgets.mount', selector => chat?.api.mount(String(selector)))
  bridge.handle('widgets.open', selector => chat?.api.open(typeof selector === 'string' ? selector : undefined))
  bridge.handle('widgets.close', selector => chat?.api.close(typeof selector === 'string' ? selector : undefined))
  bridge.handle('widgets.reveal', selector => chat?.reveal(typeof selector === 'string' ? selector : undefined))
  bridge.handle('actions.blocked', names => { if (Array.isArray(names)) actions?.setBlocked(names.filter(name => typeof name === 'string')) })
  bridge.handle('inspect', snapshot)
  bridge.handle('ui.checks', () => inspectReportUI(document, window.innerWidth))
  bridge.handle('motion', seconds => reportMotionCapture(document, typeof seconds === 'number' ? seconds : undefined))
  bridge.handle('fonts', async () => { await document.fonts.ready; return true })
  document.addEventListener('click', event => {
    const link = (event.target as Element | null)?.closest<HTMLAnchorElement>('a[href]')
    const href = link?.getAttribute('href')
    if (href && !href.startsWith('#') && !link?.hasAttribute(REPORT_OPEN_ATTR)) { event.preventDefault(); event.stopImmediatePropagation(); void bridge.request('link.open', [href]) }
    publishState()
  }, true)
  const start = async () => {
    apply(await bridge.request('initialize') as ReportDocumentContext, true)
    new ResizeObserver(publishState).observe(document.documentElement)
    new MutationObserver(publishState).observe(document.documentElement, { attributes: true, attributeFilter: ['data-report-state', 'data-report-fit', 'aria-selected', 'data-state', 'class'], childList: true, subtree: true, characterData: true })
    window.addEventListener('resize', publishState)
  }
  if (document.readyState === 'loading') document.addEventListener('DOMContentLoaded', () => { void start() }, { once: true })
  else void start()
}
