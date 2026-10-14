// @vitest-environment happy-dom
import React, { act } from 'react'
import { createRoot } from 'react-dom/client'
import { afterEach, expect, it, vi } from 'vitest'
import { DashboardQuestionConversation } from './DashboardQuestionConversation'
import { ReportEmbedProvider, type ReportDataApi } from './reportEmbedContext'

const { get, openAsk, send, auth } = vi.hoisted(() => ({ get: vi.fn(), openAsk: vi.fn(), send: vi.fn(), auth: { id: 'reader' } }))
vi.mock('../../../services/api', () => ({ default: { get } }))
vi.mock('../../../utils/projectAskConversation', () => ({ openProjectAskConversation: openAsk }))
vi.mock('../../../utils/workspacePaneChat', () => ({ sendWorkspacePaneMessageToChat: send }))
vi.mock('../../../stores/useAuthStore', () => ({ useAuthStore: (select: (state: unknown) => unknown) => select({ user: { id: auth.id } }) }))
vi.mock('../../../stores/useChatStore', () => ({ useChatStore: { getState: () => ({ addToast: vi.fn() }) } }))
vi.mock('../../ChatArea', () => ({ default: ({ tabId }: { tabId: string }) => <div data-native-tab={tabId}>Existing project history and replies</div> }))
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

it('opens a managed Relay dashboard in the Builder’s existing normal conversation', async () => {
  get.mockResolvedValue({ data: { ...view, product: 'relay' } }); openAsk.mockResolvedValue('relay-builder-chat')
  const current = await mount('relay')
  await act(async () => click('Ask'))
  expect(openAsk).toHaveBeenCalledWith({ product: 'relay', projectId: 'project-1', workspacePath: 'Workflow/demo' })
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

it.each(['crew', 'workflow', 'relay'])('lets an authorized %s Run user ask about a selected value in their native conversation', async product => {
  get.mockResolvedValue({ data: { ...view, product, capability: { can_view: true, can_ask: true, mode: 'question_only' } } })
  openAsk.mockResolvedValue('existing-run-chat'); send.mockResolvedValue({ tabId: 'existing-run-chat' })
  const current = await mount(product, 'question_only')
  await editQuestion(current.frame); await submit()
  expect(openAsk).toHaveBeenCalledWith({ product, projectId: 'project-1', workspacePath: 'Workflow/demo' })
  expect(send).toHaveBeenCalledWith(expect.objectContaining({ tabId: 'existing-run-chat', message: expect.stringContaining('"text": "42"') }))
  expect(document.querySelector('[data-native-tab]')?.getAttribute('data-native-tab')).toBe('existing-run-chat')
  expect(document.body.textContent).toContain('Existing project history and replies')
})

it.each([['crew', false], ['workflow', false], ['relay', false], ['code', true]] as const)('keeps %s Ask unavailable when capability is %s or no native adapter exists', async (product, canAsk) => {
  const current = await mount(product, 'question_only', canAsk)
  expect(current.host.querySelector('[aria-label="Open dashboard chat"]')).toBeNull()
  expect(get).not.toHaveBeenCalled(); expect(openAsk).not.toHaveBeenCalled(); expect(send).not.toHaveBeenCalled()
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
  expect(openAsk).toHaveBeenCalledWith({ product: 'workflow', projectId: 'project-1', workspacePath: 'Workflow/demo' })
  expect(document.querySelector('[data-native-tab]')?.getAttribute('data-native-tab')).toBe('existing-project-chat')
  expect(document.body.textContent).toContain('Existing project history and replies')
  expect(document.querySelector('dialog form')).toBeNull()
})

it('rechecks Ask availability before opening or submitting to the project chat', async () => {
  get.mockResolvedValue({ data: { ...view, capability: { can_view: true, can_ask: false } } })
  const current = await mount()
  await editQuestion(current.frame); await submit()
  expect(openAsk).not.toHaveBeenCalled(); expect(send).not.toHaveBeenCalled()
  expect(document.querySelector('[role="alert"]')?.textContent).toContain('no longer available')
  expect(document.querySelector('[data-native-tab]')).toBeNull()
})

it('does not show a previous account’s Ask after an in-flight conversation lookup', async () => {
  get.mockResolvedValue({ data: view })
  let finish!: (id: string) => void
  openAsk.mockImplementation(() => new Promise<string>(resolve => { finish = resolve }))
  const current = await mount()
  await act(async () => click('Ask'))
  auth.id = 'other'
  await current.render()
  await act(async () => finish('previous-account-chat'))
  expect(document.querySelector('[data-native-tab]')).toBeNull()
  expect(send).not.toHaveBeenCalled()
})

it('renders native chat in a designer-selected slot in the host document without appending a layout', async () => {
  get.mockResolvedValue({ data: view }); openAsk.mockResolvedValue('designer-chat')
  const current = await mount('workflow', 'owner', true, '<p id="total">42</p><section id="custom" data-report-chat-panel></section><button data-report-chat-button data-report-chat-target="#custom">Help</button>')
  expect(current.host.querySelector('[aria-label="Open dashboard chat"]')).toBeNull()
  expect(document.querySelector('[data-native-tab]')?.getAttribute('data-native-tab')).toBe('designer-chat')
  expect(current.frame.contentDocument!.querySelector('[data-native-tab]')).toBeNull()
  expect(current.host.querySelector<HTMLElement>('[aria-label="Dashboard chat widget"]')?.style.left).toBe('200px')
  expect(current.host.querySelector<HTMLElement>('[aria-label="Dashboard chat widget"]')?.style.width).toBe('360px')
  expect(current.host.textContent).toContain('Existing project history and replies')
  expect(send).not.toHaveBeenCalled()
})
