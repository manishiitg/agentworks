// @vitest-environment happy-dom
import React, { act } from 'react'
import { createRoot } from 'react-dom/client'
import { afterEach, expect, it, vi } from 'vitest'
import { DashboardQuestionConversation } from './DashboardQuestionConversation'
import { ReportEmbedProvider, type ReportDataApi } from './reportEmbedContext'

const { get, post, openAsk, send, auth } = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn(), openAsk: vi.fn(), send: vi.fn(), auth: { id: 'reader' } }))
vi.mock('../../../services/api', () => ({ default: { get, post } }))
vi.mock('../../../utils/projectAskConversation', () => ({ openProjectAskConversation: openAsk }))
vi.mock('../../../utils/workspacePaneChat', () => ({ sendWorkspacePaneMessageToChat: send }))
vi.mock('../../../stores/useAuthStore', () => ({ useAuthStore: (select: (state: unknown) => unknown) => select({ user: { id: auth.id } }) }))
vi.mock('../../../stores/useChatStore', () => ({ useChatStore: { getState: () => ({ addToast: vi.fn() }) } }))
vi.mock('../../ChatArea', () => ({ default: ({ tabId, slashCommands = [] }: { tabId: string; slashCommands?: { command: string; execute: () => void }[] }) => <div data-native-tab={tabId}>Existing project history and replies{slashCommands.map(command => <button key={command.command} onClick={command.execute}>/{command.command}</button>)}</div> }))
vi.mock('./reportHostRuntime', () => ({ withReportBootstrap: (html: string) => html, applyReportTheme: vi.fn(), installReportHost: vi.fn() }))
Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
const cleanup: (() => Promise<void>)[] = []
afterEach(async () => { for (const close of cleanup.splice(0)) await close(); vi.resetAllMocks(); auth.id = 'reader' })
const revision = 'a'.repeat(64)
const view = { project_id: 'project-1', product: 'workflow', capability: { can_view: true, can_ask: true, mode: 'owner' } }

async function mount(product = 'workflow', mode = 'owner', canAsk = true, markup = '<p id="total">42</p>') {
  const host = document.createElement('div'); document.body.append(host)
  const root = createRoot(host)
  cleanup.push(async () => { await act(async () => root.unmount()); host.remove() })
  const render = async (publication = revision) => {
    const data = { workspacePath: 'Workflow/demo', sendChatMessage: vi.fn() } as unknown as ReportDataApi
    await act(async () => root.render(<ReportEmbedProvider value={{ data }}>
      <DashboardQuestionConversation resourceId="dashboard-1" revision={publication} product={product} projectId="project-1" workspacePath="Workflow/demo" capability={{ can_view: true, can_ask: canAsk, mode }} html="<p>Total 42</p>" title="Overview" documentPath="db/reports/managed/overview/index.html" className="" />
    </ReportEmbedProvider>))
  }
  await render()
  const frame = host.querySelector('iframe')!
  frame.contentDocument!.body.innerHTML = markup
  vi.spyOn(frame, 'getBoundingClientRect').mockReturnValue({ left: 0, top: 0, width: 600, height: 500, right: 600, bottom: 500 } as DOMRect)
  const slot = frame.contentDocument!.querySelector('[data-report-chat-panel]')
  if (slot) vi.spyOn(slot, 'getBoundingClientRect').mockReturnValue({ left: 200, top: 20, width: 360, height: 350, right: 560, bottom: 370 } as DOMRect)
  await act(async () => frame.dispatchEvent(new Event('load')))
  return { host, frame, render }
}
function click(text: string) {
  const button = [...document.querySelectorAll('button')].find(item => item.textContent?.trim() === text)
  if (!button) throw new Error(`Missing ${text}`)
  button.click()
}

it('opens a shared Relay dashboard in the recipient’s existing normal conversation', async () => {
  get.mockResolvedValue({ data: { ...view, product: 'relay' } }); openAsk.mockResolvedValue('relay-builder-chat')
  const current = await mount('relay', 'question_only')
  await act(async () => click('Ask'))
  expect(openAsk).toHaveBeenCalledWith({ product: 'relay', projectId: 'project-1', workspacePath: 'Workflow/demo' }, { activate: false })
  expect(document.querySelector('[data-native-tab]')?.getAttribute('data-native-tab')).toBe('relay-builder-chat')
  expect(current.host.textContent).not.toContain('Open project Ask')
  expect(send).not.toHaveBeenCalled()
})
async function editQuestion(frame: HTMLIFrameElement) {
  const event = new MouseEvent('contextmenu', { bubbles: true, cancelable: true })
  Object.defineProperty(event, 'isTrusted', { value: true })
  await act(async () => frame.contentDocument!.querySelector('#total')!.dispatchEvent(event))
  await act(async () => click('Ask agent about this'))
}
async function submit() {
  await act(async () => document.querySelector('dialog form')!.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true })))
}

it.each(['crew', 'code', 'workflow', 'relay'])('lets an authorized %s Run user ask about a selected value in their native conversation', async product => {
  get.mockResolvedValue({ data: { ...view, product, capability: { can_view: true, can_ask: true, mode: 'question_only' } } })
  openAsk.mockResolvedValue('existing-run-chat'); send.mockResolvedValue({ tabId: 'existing-run-chat' })
  const current = await mount(product, 'question_only')
  await editQuestion(current.frame); await submit()
  expect(openAsk).toHaveBeenCalledWith({ product, projectId: 'project-1', workspacePath: 'Workflow/demo' }, { activate: false })
  expect(send).toHaveBeenCalledWith(expect.objectContaining({ tabId: 'existing-run-chat', message: expect.stringContaining('"text": "42"') }))
  expect(document.querySelector('[data-native-tab]')?.getAttribute('data-native-tab')).toBe('existing-run-chat')
  expect(document.body.textContent).toContain('Existing project history and replies')
})

it.each([['crew', false], ['code', false], ['workflow', false], ['relay', false], ['unknown', true]] as const)('keeps %s Ask unavailable when capability is %s or no native adapter exists', async (product, canAsk) => {
  const current = await mount(product, 'question_only', canAsk)
  expect(current.host.querySelector('[aria-label="Open dashboard chat"]')).toBeNull()
  expect(get.mock.calls.filter(([url]) => !String(url).endsWith('/functions'))).toEqual([]); expect(openAsk).not.toHaveBeenCalled(); expect(send).not.toHaveBeenCalled()
})

it('uses the existing Ask tab and preserves the clicked publication across refresh and retry', async () => {
  get.mockResolvedValue({ data: view }); openAsk.mockResolvedValue('existing-project-chat')
  send.mockRejectedValueOnce(new Error('Chat unavailable')).mockResolvedValue({ tabId: 'existing-project-chat', reused: true })
  const current = await mount()
  await editQuestion(current.frame)
  await current.render('b'.repeat(64))
  await submit()
  const original = send.mock.calls[0][0]
  expect(original.tabId).toBe('existing-project-chat')
  expect(original.notificationId).toMatch(/^dashboard-question:/)
  expect(original.message).toContain('"dashboard_id": "dashboard-1"')
  expect(original.message).toContain(`"captured_revision": "${revision}"`)
  expect(original.message).toContain('"text": "42"')
  expect(original.message).not.toContain('b'.repeat(64))
  await submit()
  expect(send.mock.calls[1][0]).toEqual(original)
  expect(openAsk).toHaveBeenCalledWith({ product: 'workflow', projectId: 'project-1', workspacePath: 'Workflow/demo' }, { activate: false })
  expect(document.querySelector('[data-native-tab]')?.getAttribute('data-native-tab')).toBe('existing-project-chat')
  expect(document.body.textContent).toContain('Existing project history and replies')
  expect(document.querySelector('dialog form')).toBeNull()
})

it.each(['crew', 'code', 'workflow'])('rechecks %s Ask availability before opening or submitting to the project chat', async product => {
  get.mockResolvedValue({ data: { ...view, product, capability: { can_view: true, can_ask: false } } })
  const current = await mount(product)
  await editQuestion(current.frame); await submit()
  expect(openAsk).not.toHaveBeenCalled(); expect(send).not.toHaveBeenCalled()
  expect(document.querySelector('[role="alert"]')?.textContent).toContain('no longer available')
  expect(document.querySelector('[data-native-tab]')).toBeNull()
})

it('does not show a previous account’s Ask after an in-flight conversation lookup', async () => {
  get.mockResolvedValue({ data: view })
  let finish!: (id: string) => void
  openAsk.mockImplementation(() => new Promise<string>(resolve => { finish = resolve }))
  const current = await mount('workflow', 'question_only')
  await act(async () => click('Ask'))
  auth.id = 'other'
  await current.render()
  await act(async () => finish('previous-account-chat'))
  expect(document.querySelector('[data-native-tab]')).toBeNull()
  expect(send).not.toHaveBeenCalled()
})

it.each(['crew', 'code', 'workflow'])('renders %s native chat in a designer-selected slot in the host document without appending a layout', async product => {
  get.mockResolvedValue({ data: { ...view, product } }); openAsk.mockResolvedValue('designer-chat')
  const current = await mount(product, 'question_only', true, '<p id="total">42</p><section id="custom" data-report-chat-panel></section><button data-report-chat-button data-report-chat-target="#custom">Help</button>')
  expect(current.host.querySelector('[aria-label="Open dashboard chat"]')).toBeNull()
  expect(document.querySelector('[data-native-tab]')?.getAttribute('data-native-tab')).toBe('designer-chat')
  expect(current.frame.contentDocument!.querySelector('[data-native-tab]')).toBeNull()
  expect(current.host.querySelector<HTMLElement>('[aria-label="Dashboard chat widget"]')?.style.left).toBe('200px')
  expect(current.host.querySelector<HTMLElement>('[aria-label="Dashboard chat widget"]')?.style.width).toBe('360px')
  expect(current.host.textContent).toContain('Existing project history and replies')
  expect(send).not.toHaveBeenCalled()
})

it.each([['crew', 'owner'], ['code', 'owner'], ['crew', 'question_only'], ['code', 'question_only']])('uses the same native function form and chat menu for a %s-origin Crew in %s mode', async (product, mode) => {
  const functions = [{ name: 'count_items', input_schema: { type: 'object', properties: { count: { type: 'integer' } }, required: ['count'] } }, { name: 'restricted', can_call: false }]
  get.mockImplementation(async (url: string) => ({ data: url.endsWith('/functions') ? { functions } : { ...view, product, capability: { can_view: true, can_ask: true, mode } } }))
  post.mockResolvedValue({ data: { status: 'completed', result: { count: 3 } } })
  openAsk.mockResolvedValue('caller-chat')
  const current = await mount(product, mode, true, '<p id="total">42</p><button data-report-action="count_items" data-report-action-defaults=\'{"count":3}\'>Count items</button><button data-report-action="restricted">Restricted</button>')
  const buttons = current.frame.contentDocument!.querySelectorAll('button')
  expect(buttons[0].disabled).toBe(false)
  expect(buttons[1].disabled).toBe(true)
  expect(post).not.toHaveBeenCalled()
  await act(async () => buttons[0].click())
  expect(document.querySelector('[data-testid="report-action-dialog"]')).not.toBeNull()
  expect(post).not.toHaveBeenCalled()
  await act(async () => document.querySelector('[data-testid="report-action-dialog"] form')!.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true })))
  expect(post).toHaveBeenCalledExactlyOnceWith('/api/dashboards/dashboard-1/functions/call', { function: 'count_items', args: { count: 3 }, submission_id: expect.any(String) })
  expect(document.querySelector('[data-testid="report-action-result"]')?.textContent).toContain('"count": 3')
  await act(async () => click('Close'))
  if (mode === 'owner') { await editQuestion(current.frame); await submit() }
  else await act(async () => click('Ask'))
  expect(openAsk).toHaveBeenCalledWith({ product, projectId: 'project-1', workspacePath: 'Workflow/demo' }, { activate: false })
  await act(async () => click('/count_items'))
  expect(document.querySelector('[data-testid="report-action-dialog"]')).not.toBeNull()
  expect(post).toHaveBeenCalledTimes(1)
})

it('omits the automatic Ask overlay for the owner while retaining contextual dashboard questions', async () => {
  get.mockResolvedValue({ data: view }); openAsk.mockResolvedValue('owner-chat'); send.mockResolvedValue({ tabId: 'owner-chat' })
  const current = await mount()
  expect(current.host.querySelector('[aria-label="Open dashboard chat"]')).toBeNull()
  expect(openAsk).not.toHaveBeenCalled()
  await editQuestion(current.frame); await submit()
  expect(openAsk).toHaveBeenCalledWith({ product: 'workflow', projectId: 'project-1', workspacePath: 'Workflow/demo' }, { activate: false })
  expect(send).toHaveBeenCalledOnce()
})

// Regression: authored controls previously bypassed the owner-only overlay rule.
it.each(['crew', 'code', 'workflow', 'relay'])('withholds authored %s Ask widgets from Builders without removing contextual questions', async product => {
  const current = await mount(product, 'owner', true, '<p id="total">42</p><section data-report-chat-panel></section><button data-report-chat-button>Ask about this dashboard</button>')
  expect(current.host.querySelector('[aria-label="Dashboard chat widget"]')).toBeNull()
  expect(current.frame.contentDocument!.head.textContent).toContain('display: none !important')
  expect(openAsk).not.toHaveBeenCalled()
  expect(send).not.toHaveBeenCalled()
})
