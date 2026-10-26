import { isReportDocumentMessage } from '../components/workflow/reportWidgets/reportDocumentBridge'
import { DashboardHostBridge, resultData } from './bridge'
import { withReportBootstrap } from '../components/workflow/reportWidgets/reportHostRuntime'
import { actionArgs, buildActionFields } from '../components/workflow/reportWidgets/reportActionForm'

type Dashboard = { resource_id: string; workspace_id: string; revision: string; title: string; url: string; html: string; asset_prefix: string; files: Record<string, string>; capability: { can_ask: boolean; mode: string } }
type PublishedFunction = { name: string; description?: string; input_schema?: unknown; can_call?: boolean }
const runtimes = JSON.parse(document.getElementById('aw-runtimes')!.textContent!) as Record<string, string>
const bridge = new DashboardHostBridge()
let dashboard: Dashboard | undefined
let theme: 'dark' | 'light' = 'light'
let channel = ''
let chatId: string | undefined
let after = 0
let chatSubmission = crypto.randomUUID()
let submittedMessage = ''
let functions: PublishedFunction[] = []
let frame: HTMLIFrameElement
let ask: HTMLTextAreaElement
let history: HTMLElement
let status: HTMLElement
let chatDialog: HTMLDialogElement

const element = <K extends keyof HTMLElementTagNameMap>(tag: K, text = '') => { const node = document.createElement(tag); node.textContent = text; return node }
const display = (value: unknown) => typeof value === 'string' ? value : JSON.stringify(value, null, 2)
const error = (reason: unknown) => { status.textContent = reason instanceof Error ? reason.message : String(reason) }
const inlineScript = (source: string) => source.replace(/<\/script/gi, '<\\/script')
function structuredFileURL(source: string) {
  if (source.startsWith('data:')) return source
  return URL.createObjectURL(new Blob([source], { type: 'text/plain' }))
}

function renderDocument() {
  if (!dashboard) return
  channel = crypto.randomUUID()
  const page = new DOMParser().parseFromString(withReportBootstrap(dashboard.html), 'text/html')
  // Trusted renderer assets travel in the resource; the sandbox never receives
  // an authorization token or fetches the app's authenticated static routes.
  for (const script of page.querySelectorAll<HTMLScriptElement>('script[src]')) {
    const src = script.getAttribute('src') || ''
    const name = src.endsWith('/dashboard-react.js') ? 'dashboard-react.js' : ''
    const source = name ? runtimes[name] : dashboard.files[src.replace(dashboard.asset_prefix + '/', '')]
    if (source) {
      script.removeAttribute('src')
      // Removing src makes a classic deferred script execute in <head> too
      // early. Keep its global execution, but wait for the authored DOM.
      const code = script.defer && script.type !== 'module'
        ? `document.addEventListener('DOMContentLoaded',()=>{const s=document.createElement('script');s.textContent=${JSON.stringify(source).replace(/</g, '\\u003c')};document.head.append(s)},{once:true})`
        : source
      script.textContent = inlineScript(code)
    }
    else { script.remove(); status.textContent = 'An external dashboard asset is unavailable here. Open the website link for the full page.' }
  }
  for (const style of page.querySelectorAll<HTMLLinkElement>('link[rel="stylesheet"]')) {
    const href = style.getAttribute('href') || ''
    const source = href.endsWith('/dashboard.css') ? runtimes['dashboard.css'] : dashboard.files[href.replace(dashboard.asset_prefix + '/', '')]
    if (source) { const inline = element('style'); inline.textContent = source; style.replaceWith(inline) }
    else style.remove()
  }
  for (const image of page.querySelectorAll<HTMLImageElement>('img[src]')) {
    const src = image.getAttribute('src') || ''
    const data = dashboard.files[src.replace(dashboard.asset_prefix + '/', '')]
    if (data) image.src = structuredFileURL(data)
  }
  const config = element('script')
  config.id = 'aw-document-config'
  config.type = 'application/json'
  config.textContent = JSON.stringify({ channel, title: dashboard.title, workspacePath: dashboard.workspace_id, canAsk: dashboard.capability.can_ask && dashboard.capability.mode === 'question_only', theme }).replace(/</g, '\\u003c')
  const runtime = element('script')
  runtime.textContent = inlineScript(runtimes.document)
  page.head.prepend(config, runtime)
  const csp = element('meta')
  csp.httpEquiv = 'Content-Security-Policy'
  csp.content = "default-src 'none'; script-src 'unsafe-inline' blob:; style-src 'unsafe-inline' blob:; img-src data: blob:; font-src data: blob:; connect-src 'none'; frame-src 'none'; form-action 'none'; base-uri 'none'"
  page.head.prepend(csp)
  const next = element('iframe')
  next.title = dashboard.title
  next.sandbox.add('allow-scripts')
  next.onload = () => {
    next.onload = null
    next.contentWindow?.postMessage({ kind: 'agentworks-dashboard-document', html: '<!doctype html>' + page.documentElement.outerHTML }, '*')
  }
  next.src = runtimes.frame_url
  frame.replaceWith(next)
  frame = next
}

async function listFunctions() {
  if (!dashboard) return
  const value = await bridge.call('functions', { action: 'list', crew_id: dashboard.workspace_id })
  functions = (value.functions ?? []) as PublishedFunction[]

}

function showFunction(name: string, defaults: Record<string, unknown> = {}) {
  const info = functions.find(item => item.name === name)
  if (!info || info.can_call === false || !dashboard?.capability.can_ask) { error('This action is unavailable. Open AgentWorks to inspect the published actions.'); return }
  const dialog = element('dialog')
  const form = element('form')
  const fields = buildActionFields(info.input_schema, defaults)
  const output = element('pre')
  const submission = crypto.randomUUID()
  let submittedArgs = ''
  let currentSubmission = submission
  form.append(element('h2', info.name.replaceAll('_', ' ')), element('p', info.description || ''))
  for (const field of fields) {
    const label = element('label', field.label + (field.required ? ' *' : ''))
    const input = field.kind === 'json' ? element('textarea') : element('input')
    if (input instanceof HTMLInputElement) input.type = field.kind === 'boolean' ? 'checkbox' : 'text'
    if (input instanceof HTMLInputElement && field.kind === 'boolean') input.checked = field.value === true
    else input.value = String(field.value ?? '')
    input.oninput = () => { field.value = input instanceof HTMLInputElement && field.kind === 'boolean' ? input.checked : input.value }
    label.append(input); form.append(label)
  }
  const submit = element('button', 'Run')
  submit.type = 'submit'
  const close = element('button', 'Close')
  close.type = 'button'; close.onclick = () => { dialog.close(); dialog.remove() }
  form.append(submit, close, output)
  let callId = ''
  const check = element('button', 'Check progress')
  check.type = 'button'; check.hidden = true
  check.onclick = async () => {
    try { const value = await bridge.call('functions', { action: 'status', crew_id: dashboard!.workspace_id, call_id: callId, wait_seconds: 2 }); output.textContent = display(value.answer ?? value.result ?? value.error ?? value.status ?? 'Working…') } catch (reason) { output.textContent = display(String(reason)) }
  }
  form.append(check)
  form.onsubmit = async event => {
    event.preventDefault(); submit.disabled = true
    try {
      const input = actionArgs(fields)
      if (!input.ok) throw new Error(input.error)
      const signature = JSON.stringify(input.args)
      if (submittedArgs && submittedArgs !== signature) currentSubmission = crypto.randomUUID()
      submittedArgs = signature
      const value = await bridge.call('functions', { action: 'call', crew_id: dashboard!.workspace_id, function: name, args: input.args, submission_id: currentSubmission, wait_seconds: 2 })
      callId = String(value.call_id || ''); check.hidden = !callId
      output.textContent = display(value.answer ?? value.result ?? value.error ?? value.status ?? 'Accepted. Check progress.')
    } catch (reason) { output.textContent = reason instanceof Error ? reason.message : String(reason); submit.disabled = false }
  }
  dialog.append(form); document.body.append(dialog); dialog.showModal()
}

function appendMessages(value: Record<string, unknown>) {
  for (const message of (value.messages ?? []) as { role?: string; text?: string }[]) {
    if (message.text) history.append(element('p', `${message.role === 'you' ? 'You' : 'Crew'}: ${message.text}`))
  }
  if (typeof value.next_after === 'number') after = value.next_after
  if (typeof value.chat_id === 'string') chatId = value.chat_id
  status.textContent = value.working === false ? 'Ready' : value.working ? 'Working. Use Check reply to read progress.' : 'Accepted. Use Check reply to read progress.'
}

function mount() {
  const app = document.getElementById('app')!
  const style = element('style', 'body{margin:0;font:14px system-ui;color:var(--color-text-primary,#202124);background:var(--color-background-primary,#fff)}header,section{padding:12px}button,input,textarea,select{font:inherit;padding:8px;border:1px solid #aaa;border-radius:7px;margin:3px}button{cursor:pointer}button:disabled{cursor:default;opacity:.6}iframe{width:100%;border:0;display:block;min-height:220px}textarea{box-sizing:border-box;width:100%;min-height:70px}label{display:block;margin:8px 0}label input,label textarea{display:block;width:90%}pre{white-space:pre-wrap;overflow-wrap:anywhere}dialog{max-width:560px;width:85%;border:1px solid #aaa;border-radius:12px}#history{max-height:350px;overflow:auto}a{color:inherit}body.dark{color-scheme:dark}')
  document.head.append(style)
  const header = element('header')
  const title = element('strong', dashboard!.title)
  const link = element('button', 'Open in AgentWorks'); link.type = 'button'
  link.onclick = () => { void bridge.request('ui/open-link', { url: dashboard!.url }).catch(error) }
  const refresh = element('button', 'Refresh'); refresh.type = 'button'
  refresh.onclick = () => { void bridge.tool('open_dashboard', { resource_id: dashboard!.resource_id }).then(value => receive(value)).catch(error) }
  header.append(title, link, refresh)
  frame = element('iframe'); frame.title = dashboard!.title; frame.sandbox.add('allow-scripts')
  chatDialog?.remove()
  chatDialog = element('dialog'); chatDialog.setAttribute('aria-label', 'Ask about this dashboard')
  const form = element('form'); form.append(element('h2', 'Ask about this dashboard'))
  ask = element('textarea'); ask.setAttribute('aria-label', 'Message to the Crew'); ask.disabled = !dashboard!.capability.can_ask
  const send = element('button', 'Send'); send.type = 'submit'; send.disabled = ask.disabled
  const check = element('button', 'Check reply'); check.type = 'button'
  check.onclick = () => { if (chatId) void bridge.call('workspace', { action: 'read_chat', workspace_id: dashboard!.workspace_id, chat_id: chatId, after, wait_seconds: 2 }).then(appendMessages).catch(error) }
  status = element('p', 'Ready'); status.setAttribute('role', 'status')
  history = element('div'); history.id = 'history'
  form.onsubmit = async event => {
    event.preventDefault(); if (!ask.value.trim()) return
    send.disabled = true
    try {
      if (submittedMessage && submittedMessage !== ask.value) chatSubmission = crypto.randomUUID()
      submittedMessage = ask.value
      const value = await bridge.call('workspace', { action: 'ask', workspace_id: dashboard!.workspace_id, ...(chatId ? { chat_id: chatId } : {}), message: ask.value, submission_id: chatSubmission, wait_seconds: 2 })
      appendMessages(value); ask.value = ''; chatSubmission = crypto.randomUUID(); submittedMessage = ''
    } catch (reason) { error(reason) } finally { send.disabled = !dashboard!.capability.can_ask }
  }
  const close = element('button', 'Close'); close.type = 'button'; close.onclick = () => chatDialog.close()
  form.append(ask, send, check, close, history); chatDialog.append(form)
  header.append(status); app.replaceChildren(header, frame); document.body.append(chatDialog)
  renderDocument()
  if (dashboard!.capability.can_ask) void listFunctions().catch(error)
  new ResizeObserver(() => bridge.notify('ui/notifications/size-changed', { height: document.documentElement.scrollHeight })).observe(app)
}

function receive(value: { _meta?: Record<string, unknown> }) {
  const next = value._meta?.dashboard as Dashboard | undefined
  if (!next?.resource_id || typeof next.html !== 'string') return
  const changed = dashboard?.resource_id !== next.resource_id
  dashboard = next
  if (changed) { chatId = undefined; after = 0; chatSubmission = crypto.randomUUID(); submittedMessage = ''; mount() }
  else { renderDocument(); if (dashboard.capability.can_ask) void listFunctions().catch(error) }
}
bridge.onResult = receive
bridge.onContext = context => { theme = context.theme ?? theme; document.body.classList.toggle('dark', theme === 'dark'); frame?.contentWindow?.postMessage({ channel, method: 'theme', args: [theme] }, '*') }
window.addEventListener('message', async event => {
  if (!dashboard || !isReportDocumentMessage(event, frame?.contentWindow ?? null, channel)) return
  const { id, method, args } = event.data as { id: number; method: string; args: unknown[] }
  const target = event.source as Window
  const requestChannel = channel
  try {
    let value: unknown
    if (method === 'query' || method === 'file') {
      value = resultData(await bridge.tool('read_dashboard_data', { resource_id: dashboard.resource_id, revision: dashboard.revision, action: method, ...(method === 'query' ? { sql: args[0], params: args[1] ?? [] } : { path: args[0] }) }))
    } else if (method === 'costs') {
      const options = (args[0] ?? {}) as { days?: number; before?: string }
      value = await bridge.call('workspace', { action: 'costs', workspace_id: dashboard.workspace_id, ...(options.days ? { days: options.days } : {}), ...(options.before ? { before: options.before } : {}) })
      const costs = value as Record<string, unknown>
      value = { scoped_costs: { total: costs.total, by_date: costs.by_date, by_model: costs.by_model, by_scope: costs.by_scope, by_source: costs.by_source } }
    }
    else if (method === 'chat.open') { if (typeof args[0] === 'string') ask.value = args[0]; if (!chatDialog.open) chatDialog.showModal(); ask.focus(); ask.scrollIntoView({ block: 'nearest' }) }
    else if (method === 'chat.close') chatDialog.close()
    else if (method === 'function.open') showFunction(String(args[0]), args[1] as Record<string, unknown>)
    else if (method === 'size') frame.style.height = `${Math.min(4000, Math.max(220, Number(args[0]) || 220))}px`
    else if (method === 'file.open') throw new Error('Open the website dashboard to preview this file.')
    else if (method === 'link.open') {
      if (typeof args[0] !== 'string' || !/^(https?:|mailto:)/i.test(args[0])) throw new Error('Choose a website or email link.')
      await bridge.request('ui/open-link', { url: args[0] })
    }
    else throw new Error('This operation is unavailable in the inline dashboard.')
    target.postMessage({ channel: requestChannel, id, value }, '*')
  } catch (reason) { target.postMessage({ channel: requestChannel, id, error: reason instanceof Error ? reason.message : String(reason) }, '*') }
})
void bridge.connect().catch(reason => { document.getElementById('app')!.textContent = reason instanceof Error ? reason.message : String(reason) })
