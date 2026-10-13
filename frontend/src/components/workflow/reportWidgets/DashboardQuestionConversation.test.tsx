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

async function mount() {
  const host = document.createElement('div'); document.body.append(host)
  const root = createRoot(host)
  cleanup.push(async () => { await act(async () => root.unmount()); host.remove() })
  const render = async (publication = revision) => {
    const data = { workspacePath: 'Workflow/demo', sendChatMessage: vi.fn() } as unknown as ReportDataApi
    await act(async () => root.render(<ReportEmbedProvider value={{ data }}>
      <DashboardQuestionConversation resourceId="dashboard-1" revision={publication} product="workflow" projectId="project-1" workspacePath="Workflow/demo" capability={{ can_view: true, can_ask: true, mode: 'owner' }} html="<p>Total 42</p>" title="Overview" documentPath="db/reports/managed/overview/index.html" className="" />
    </ReportEmbedProvider>))
  }
  await render()
  const frame = host.querySelector('iframe')!
  frame.contentDocument!.body.innerHTML = '<p id="total">42</p>'
  await act(async () => frame.dispatchEvent(new Event('load')))
  return { host, frame, render }
}
function click(text: string) {
  const button = [...document.querySelectorAll('button')].find(item => item.textContent?.trim() === text)
  if (!button) throw new Error(`Missing ${text}`)
  button.click()
}
async function editQuestion(frame: HTMLIFrameElement) {
  const event = new MouseEvent('contextmenu', { bubbles: true, cancelable: true })
  Object.defineProperty(event, 'isTrusted', { value: true })
  await act(async () => frame.contentDocument!.querySelector('#total')!.dispatchEvent(event))
  await act(async () => click('Ask agent about this'))
}
async function submit() {
  await act(async () => document.querySelector('dialog form')!.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true })))
}

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
  expect(current.host.querySelector('[data-native-tab]')?.getAttribute('data-native-tab')).toBe('existing-project-chat')
  expect(current.host.textContent).toContain('Existing project history and replies')
  expect(document.querySelector('dialog')).toBeNull()
})

it('rechecks Ask availability before opening or submitting to the project chat', async () => {
  get.mockResolvedValue({ data: { ...view, capability: { can_view: true, can_ask: false } } })
  const current = await mount()
  await editQuestion(current.frame); await submit()
  expect(openAsk).not.toHaveBeenCalled(); expect(send).not.toHaveBeenCalled()
  expect(document.querySelector('[role="alert"]')?.textContent).toContain('no longer available')
  expect(current.host.querySelector('[data-native-tab]')).toBeNull()
})

it('does not show a previous account’s Ask after an in-flight conversation lookup', async () => {
  get.mockResolvedValue({ data: view })
  let finish!: (id: string) => void
  openAsk.mockImplementation(() => new Promise<string>(resolve => { finish = resolve }))
  const current = await mount()
  await act(async () => click('Open project Ask'))
  auth.id = 'other'
  await current.render()
  await act(async () => finish('previous-account-chat'))
  expect(current.host.querySelector('[data-native-tab]')).toBeNull()
  expect(send).not.toHaveBeenCalled()
})
