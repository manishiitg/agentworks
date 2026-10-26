// DOM-emulator adapter for existing native conversation tests. These tests do
// not prove origin isolation; test-dashboard-isolation-browser.mjs does that.
import { installReportDocumentHost, readReportDocumentState } from './reportHostRuntime'
import { installDashboardChatWidgets } from './dashboardChatWidgets'
import { installDashboardActionButtons } from './reportActionButtons'
import { bindReportContentMenu } from './reportContentQuestion'
import type { ReportFrameOptions, ReportDocumentSnapshot } from './opaqueReportFrame'
import type { ReportDocumentBridge } from './reportDocumentBridge'
import type { ReportHostInstallOptions } from './reportHostRuntime'

export const fixtureInstalls: ReportHostInstallOptions[] = []
export class OpaqueReportFrame {
  state: ReportDocumentSnapshot = { state: 'unloaded', errors: [], title: '', tabs: [], loadingTexts: [], markdownTexts: [], height: 0 }
  private handlers = new Map<string, (...args: unknown[]) => unknown>()
  private chat?: ReturnType<typeof installDashboardChatWidgets>
  private actions?: ReturnType<typeof installDashboardActionButtons>
  private menu?: () => void
  private focusToken?: number
  private loaded = false
  readonly bridge = {
    handle: (name: string, fn: (...args: unknown[]) => unknown) => { this.handlers.set(name, fn); return () => { if (this.handlers.get(name) === fn) this.handlers.delete(name) } },
    request: async (name: string, args: unknown[] = []) => this.invokeChild(name, args),
    notify: (name: string, args: unknown[] = []) => { this.invokeChild(name, args) },
  } as unknown as ReportDocumentBridge
  readonly frame: HTMLIFrameElement
  private options: () => ReportFrameOptions
  constructor(frame: HTMLIFrameElement, options: () => ReportFrameOptions) {
    this.frame = frame; this.options = options
 frame.addEventListener('load', this.install) }
  private parent = async (name: string, args: unknown[] = []) => this.handlers.get(name)?.(...args)
  private invokeChild(name: string, args: unknown[]) {
    if (name === 'actions.blocked') this.actions?.setBlocked(args[0] as string[])
    if (name === 'widgets.mount') return this.chat?.api.mount(args[0] as string)
    if (name === 'widgets.open') return this.chat?.api.open(args[0] as string)
    if (name === 'widgets.close') return this.chat?.api.close(args[0] as string)
    if (name === 'widgets.reveal') return this.chat?.reveal(args[0] as string)
  }
  private install = () => {
    const doc = this.frame.contentDocument!, options = this.options(), widget = options.widgets?.() ?? {}
    this.loaded = true
    this.chat?.dispose(); this.actions?.dispose(); this.menu?.()
    this.chat = installDashboardChatWidgets(doc, { available: Boolean(widget.chatAvailable), visible: widget.chatVisible,
      openConversation: () => this.parent('chat.open'), changed: (slots, authored) => { void this.parent('widgets.layout', [slots, authored]) },
      popup: open => { void this.parent('chat.popup', [open]) }, error: message => { void this.parent('chat.error', [message]) } })
    this.actions = installDashboardActionButtons(doc, { available: Boolean(widget.functionsAvailable), open: action => { void this.parent('action.open', [action]) } })
    this.actions.setBlocked(widget.blockedFunctions ?? [])
    this.menu = bindReportContentMenu(doc, menu => this.options().onContentMenu?.(menu), () => Boolean(this.options().onContentMenu))
    const install = { ...options, tokenSource: null, dispatchData: true, chatApi: this.chat.api }
    fixtureInstalls.push(install)
    installReportDocumentHost(doc, install)
    this.focus()
    this.publish()
  }
  private focus() {
    const request = this.options().focus
    if (request && request.token !== this.focusToken) { this.focusToken = request.token; this.frame.contentWindow!.dispatchEvent(new Event('report:focus')) }
  }
  private publish() {
    this.state = { ...readReportDocumentState(this.frame.contentDocument), fitViewport: this.frame.contentDocument?.documentElement.getAttribute('data-report-fit') === 'viewport' }
    this.options().onState?.(this.state)
  }
  load(_html: string) { /* Existing tests supply the synthetic document explicitly. */ }
  update(_refresh = false) { if (this.loaded) { fixtureInstalls.push({ ...this.options(), tokenSource: null, dispatchData: false }); this.focus(); this.publish() } }
  request() { return Promise.resolve(undefined) }
  dispose() { this.frame.removeEventListener('load', this.install); this.chat?.dispose(); this.actions?.dispose(); this.menu?.(); this.handlers.clear() }
}
