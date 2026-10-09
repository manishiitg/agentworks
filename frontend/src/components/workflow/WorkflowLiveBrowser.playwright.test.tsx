// @vitest-environment happy-dom
import React, { act } from 'react'
import { createRoot } from 'react-dom/client'
import { afterEach, expect, it, vi } from 'vitest'
import { BrowserWorkspacePanel } from './BrowserWorkspacePanel'
import { TooltipProvider } from '../ui/tooltip'
import { getDisplaySafeUserMessageContent } from '../../utils/chatMessageContent'
import WorkflowLiveBrowser, { BROWSER_RECONNECT_ATTEMPTS, browserReconnectDelayMs, mapToViewport } from './WorkflowLiveBrowser'
const api = vi.hoisted(() => ({ defaults: {}, get: vi.fn(), post: vi.fn() }))
vi.mock('../../services/api', () => ({ default: api, getApiBaseUrl: () => 'http://localhost', getAuthToken: () => 'viewer-token' }))
vi.mock('../../hooks/useCanWriteWorkflow', () => ({ useCanWriteWorkflow: () => true }))
vi.mock('../../stores/useChatStore', () => ({ useChatStore: { getState: () => ({ addToast: vi.fn() }) } }))
vi.mock('../../stores/useWorkflowStore', () => ({ useWorkflowStore: { getState: () => ({ openWorkspaceView: vi.fn() }) } }))
class FakeSocket {
  static OPEN = 1
  static instances: FakeSocket[] = []
  readyState = 1
  onopen?: () => void
  onmessage?: (event: { data: string }) => void
  onclose?: () => void
  send = vi.fn()
  close = vi.fn()
  readonly url: string
  constructor(url: string) { this.url = url; FakeSocket.instances.push(this); queueMicrotask(() => this.onopen?.()) }
}
const cleanups: (() => void)[] = []
afterEach(() => { cleanups.splice(0).forEach(fn => fn()); vi.unstubAllGlobals(); vi.useRealTimers(); sessionStorage.clear(); vi.clearAllMocks(); FakeSocket.instances = [] })
it('shows both browser types but makes Playwright watch-only even for workflow writers', async () => {
  vi.stubGlobal('IS_REACT_ACT_ENVIRONMENT', true)
  vi.stubGlobal('WebSocket', FakeSocket)
  api.get.mockResolvedValue({ data: { sessions: [
    { browser_session: 'pw-test', workflow_session: 'run', label: 'Checkout · retry 0', kind: 'playwright', read_only: 'true' },
    { browser_session: 'agent-test', workflow_session: 'run', label: 'Agent browser' },
  ] } })
  api.post.mockResolvedValue({ data: { recording: false } })
  const host = document.createElement('div'); document.body.append(host)
  const root = createRoot(host)
  cleanups.push(() => { act(() => root.unmount()); host.remove() })
  await act(async () => { root.render(<WorkflowLiveBrowser workspacePath="Workflow/test" />) })
  const buttons = () => [...host.querySelectorAll('button')].map(button => button.textContent)
  expect(host.textContent).toContain('Checkout · retry 0')
  expect(host.textContent).toContain('Agent browser')
  expect(host.textContent).toContain('Watch-only')
  expect(buttons()).not.toContain('Take control')
  expect(buttons()).not.toContain('Start recording')
  expect(host.querySelector('select[aria-label="Browser sizing"]')).toBeNull()
  expect(api.post).not.toHaveBeenCalled()
  await act(async () => {
    FakeSocket.instances[0].onmessage?.({ data: JSON.stringify({ type: 'frame', data: '/9j/', metadata: { deviceWidth: 640, deviceHeight: 480 } }) })
    FakeSocket.instances[0].onmessage?.({ data: JSON.stringify({ type: 'tabs', tabs: [{ tabId: 't1', title: 'Checkout', url: 'http://localhost', active: false }, { tabId: 't2', title: 'Home', url: 'http://localhost/home', active: true }] }) })
  })
  expect(host.querySelector('img')?.getAttribute('src')).toBe('data:image/jpeg;base64,/9j/')
  const tab = [...host.querySelectorAll('button')].find(button => button.textContent === 'Checkout')!
  expect(tab.disabled).toBe(true)
  const selector = host.querySelector('select[aria-label="Browser session"]') as HTMLSelectElement
  await act(async () => { selector.value = 'agent-test'; selector.dispatchEvent(new Event('change', { bubbles: true })) })
  expect(buttons()).toContain('Take control')
  expect(buttons()).not.toContain('Start recording')
  await act(async () => { (host.querySelector('button[aria-label="More browser options"]') as HTMLButtonElement).click() })
  expect(buttons()).toContain('Start recording')
})

it('fits the browser automatically while preserving its aspect ratio', async () => {
  api.get.mockResolvedValue({ data: { sessions: [testBrowser('pw-aspect')] } })
  const { host } = await mountBrowser()
  await act(async () => { FakeSocket.instances.at(-1)?.onmessage?.({ data: JSON.stringify({ type: 'frame', data: '/9j/', metadata: { deviceWidth: 900, deviceHeight: 1600 } }) }) })
  const image = host.querySelector('img')!
  // Fill the panel and letterbox: never cropped, never a tiny centered image.
  expect(image.className).toContain('object-contain')
  expect(image.className).toContain('h-full')
  expect(image.className).toContain('w-full')
})

const shared = { browser_session: 'shared-browser', workflow_session: 'shared', label: 'Shared browser · all users' }
const testBrowser = (id: string) => ({ browser_session: id, workflow_session: 'child-run', label: `Login ${id}`, kind: 'playwright', read_only: 'true' })
async function mountBrowser(fakeTimeouts = false) {
  vi.useFakeTimers({ toFake: fakeTimeouts ? ['setInterval', 'clearInterval', 'setTimeout', 'clearTimeout'] : ['setInterval', 'clearInterval'] })
  vi.stubGlobal('IS_REACT_ACT_ENVIRONMENT', true)
  vi.stubGlobal('WebSocket', FakeSocket)
  api.post.mockResolvedValue({ data: { recording: false } })
  const host = document.createElement('div'); document.body.append(host)
  const root = createRoot(host)
  cleanups.push(() => { act(() => root.unmount()); host.remove() })
  await act(async () => { root.render(<WorkflowLiveBrowser workspacePath="Workflow/test" />) })
  const picker = () => host.querySelector('select[aria-label="Browser session"]') as HTMLSelectElement
  return { root, host, picker, get selector() { return picker() } }
}
async function pollBrowsers(sessions: unknown[]) {
  api.get.mockResolvedValue({ data: { sessions } })
  await act(async () => { vi.advanceTimersByTime(1000) })
}
it('follows new Playwright cases instead of staying on the default shared blank browser', async () => {
  api.get.mockResolvedValue({ data: { sessions: [shared] } })
  const { host, picker } = await mountBrowser()
  expect(picker()).toBeNull()
  expect(host.querySelector('header')?.textContent).toContain('Shared browser · all users')
  expect(String(FakeSocket.instances.at(-1)?.url)).toContain('/shared-browser/stream')
  await pollBrowsers([shared, testBrowser('pw-one')])
  expect(picker().value).toBe('playwright-tests')
  expect(String(FakeSocket.instances.at(-1)?.url)).toContain('/pw-one/stream')
  await act(async () => { FakeSocket.instances.at(-1)?.onmessage?.({ data: JSON.stringify({ type: 'frame', data: '/9j/' }) }) })
  expect(host.querySelector('img')?.src).toBe('data:image/jpeg;base64,/9j/')
  await pollBrowsers([shared])
  expect(host.textContent).toContain('Completed')
  expect(host.querySelector('img')?.src).toBe('data:image/jpeg;base64,/9j/')
  expect(host.querySelector('img')?.alt).toBe('Last Playwright test frame')
  await pollBrowsers([shared, testBrowser('pw-two')])
  expect(picker().value).toBe('playwright-tests')
  expect(String(FakeSocket.instances.at(-1)?.url)).toContain('/pw-two/stream')
  expect(host.querySelector('img')).toBeNull()
  expect(host.textContent).not.toContain('Completed')
  await act(async () => { FakeSocket.instances.at(-1)?.onmessage?.({ data: JSON.stringify({ type: 'frame', data: '/9j/new' }) }) })
  expect(host.querySelector('img')?.src).toBe('data:image/jpeg;base64,/9j/new')
})
const crew = { browser_session: 'crew-browser', workflow_session: 'crew', label: 'Crew browser' }
it('follows the managed browser while waiting for the next Playwright test', async () => {
  api.get.mockResolvedValue({ data: { sessions: [shared, crew] } })
  const { host, selector } = await mountBrowser()
  expect([...selector.options].map(option => option.textContent)).toContain('Follow browser activity')
  await act(async () => { selector.value = 'playwright-tests'; selector.dispatchEvent(new Event('change', { bubbles: true })) })
  expect(sessionStorage.getItem('browser-selection:Workflow/test')).toBe('playwright-tests')
  await pollBrowsers([shared, crew])
  expect(selector.value).toBe('playwright-tests')
  expect(selector.selectedOptions[0].textContent).toBe('Shared browser · all users · Auto')
  expect(String(FakeSocket.instances.at(-1)?.url)).toContain('/shared-browser/stream')
  expect([...host.querySelectorAll('button')].map(button => button.textContent)).toContain('Take control')
  await pollBrowsers([shared, crew, testBrowser('pw-selected')])
  expect(String(FakeSocket.instances.at(-1)?.url)).toContain('/pw-selected/stream')
})

it('migrates a saved follow-latest-test selection to the managed Crew browser when no test is running', async () => {
  sessionStorage.setItem('browser-selection:Chats/Work/projects/gptlive1', 'playwright-tests')
  api.get.mockResolvedValue({ data: { sessions: [{ ...shared, browser_session: 'crew-browser', label: 'Persistent browser' }] } })
  vi.useFakeTimers({ toFake: ['setInterval', 'clearInterval'] })
  vi.stubGlobal('IS_REACT_ACT_ENVIRONMENT', true)
  vi.stubGlobal('WebSocket', FakeSocket)
  api.post.mockResolvedValue({ data: { recording: false } })
  const host = document.createElement('div'); document.body.append(host)
  const root = createRoot(host)
  cleanups.push(() => { act(() => root.unmount()); host.remove() })
  await act(async () => { root.render(<WorkflowLiveBrowser workspacePath="Chats/Work/projects/gptlive1" scopeNoun="project" />) })
  expect(host.querySelector('select[aria-label="Browser session"]')).toBeNull()
  expect(host.querySelector('header')?.textContent).toContain('Persistent browser')
  expect(String(FakeSocket.instances.at(-1)?.url)).toContain('/crew-browser/stream')
  expect(host.textContent).not.toContain('Waiting for a Playwright test')
})
it('respects an explicit shared-browser choice while tests are running', async () => {
  api.get.mockResolvedValue({ data: { sessions: [shared, testBrowser('pw-one')] } })
  const { selector } = await mountBrowser()
  expect(selector.value).toBe('playwright-tests')
  await act(async () => { selector.value = 'shared-browser'; selector.dispatchEvent(new Event('change', { bubbles: true })) })
  await pollBrowsers([shared, testBrowser('pw-two')])
  expect(selector.value).toBe('shared-browser')
  expect(String(FakeSocket.instances.at(-1)?.url)).toContain('/shared-browser/stream')
})

it('keeps the last frame on disconnect without claiming completion until the source disappears', async () => {
  api.get.mockResolvedValue({ data: { sessions: [shared, testBrowser('pw-one')] } })
  const { host } = await mountBrowser()
  const source = FakeSocket.instances.at(-1)!
  await act(async () => {
    source.onmessage?.({ data: JSON.stringify({ type: 'frame', data: '/9j/old' }) })
    source.onmessage?.({ data: JSON.stringify({ type: 'frame', data: '/9j/latest' }) })
    source.onclose?.()
  })
  expect(host.querySelector('img')?.src).toBe('data:image/jpeg;base64,/9j/latest')
  expect(host.textContent).toContain('Disconnected · Last frame')
  expect(host.textContent).not.toContain('Completed')
  await pollBrowsers([shared])
  expect(host.textContent).toContain('Completed · Last frame')
  expect(host.querySelector('img')?.src).toBe('data:image/jpeg;base64,/9j/latest')
})
it('clears retained frames when the workflow changes and ignores late messages from the old source', async () => {
  api.get.mockResolvedValue({ data: { sessions: [testBrowser('pw-one')] } })
  const { root, host } = await mountBrowser()
  const source = FakeSocket.instances.at(-1)!
  await act(async () => { source.onmessage?.({ data: JSON.stringify({ type: 'frame', data: '/9j/private' }) }) })
  await pollBrowsers([])
  expect(host.textContent).toContain('Completed')
  await act(async () => { root.render(<WorkflowLiveBrowser workspacePath="Workflow/other" />) })
  await act(async () => { source.onmessage?.({ data: JSON.stringify({ type: 'frame', data: '/9j/late' }) }) })
  expect(host.querySelector('img')).toBeNull()
  expect(host.textContent).not.toContain('Completed')
})
it('does not show a retained test frame when the shared browser is selected', async () => {
  api.get.mockResolvedValue({ data: { sessions: [shared, testBrowser('pw-one')] } })
  const { host, selector } = await mountBrowser()
  await act(async () => { FakeSocket.instances.at(-1)?.onmessage?.({ data: JSON.stringify({ type: 'frame', data: '/9j/test' }) }) })
  await pollBrowsers([shared, crew])
  // A live frame switches to the compact bar, which re-renders the picker.
  const current = (host.querySelector('select[aria-label="Browser session"]') as HTMLSelectElement | null) ?? selector
  await act(async () => { current.value = 'shared-browser'; current.dispatchEvent(new Event('change', { bubbles: true })) })
  expect(host.querySelector('img')).toBeNull()
  expect(host.textContent).not.toContain('Completed')
})

it('plays completed tests, offers download, and deletes the replay when the panel closes', async () => {
  const create = vi.spyOn(URL, 'createObjectURL').mockReturnValue('blob:replay')
  const revoke = vi.spyOn(URL, 'revokeObjectURL').mockImplementation(() => {})
  const completed = { ...testBrowser('pw-replay'), state: 'completed', recording_state: 'ready' }
  api.get.mockImplementation(async (url: string) => ({ data: url.endsWith('/recording') ? new Blob(['video'], { type: 'video/mp4' }) : { sessions: [shared, completed] } }))
  const { root, host } = await mountBrowser()
  expect(host.querySelector('video')?.getAttribute('src')).toBe('blob:replay')
  expect(host.querySelector('video')?.controls).toBe(true)
  expect(host.querySelector('a[download]')?.getAttribute('href')).toBe('blob:replay')
  expect(host.querySelector('[role="note"]')?.getAttribute('aria-label')).toContain('deleted when this panel closes')
  expect(host.textContent).not.toContain('Closing this panel deletes')
  expect(FakeSocket.instances).toHaveLength(0)
  expect(api.post).not.toHaveBeenCalledWith(expect.anything(), { action: 'delete' }, expect.anything())
  await act(async () => { root.unmount(); await new Promise(resolve => setTimeout(resolve, 5)) })
  expect(api.post).toHaveBeenCalledWith('/api/browser/live/pw-replay/recording', { action: 'delete' }, { params: { workspace_path: 'Workflow/test' } })
  expect(revoke).toHaveBeenCalledWith('blob:replay')
  create.mockRestore(); revoke.mockRestore()
})

it('continues following live tests when an older completed replay remains', async () => {
  api.get.mockResolvedValue({ data: { sessions: [testBrowser('pw-first')] } })
  const { selector } = await mountBrowser()
  await pollBrowsers([{ ...testBrowser('pw-first'), state: 'completed', recording_state: 'saving' }, testBrowser('pw-next')])
  expect(selector.value).toBe('playwright-tests')
  expect(String(FakeSocket.instances.at(-1)?.url)).toContain('/pw-next/stream')
})

it('shows replay processing state instead of waiting for a completed browser live view', async () => {
  api.get.mockResolvedValue({ data: { sessions: [{ ...testBrowser('pw-queued'), state: 'completed', recording_state: 'queued' }] } })
  const { host } = await mountBrowser()
  expect(host.textContent).toContain('Replay queued for processing')
  expect(host.textContent).not.toContain('Waiting for the browser’s live view')
  await pollBrowsers([{ ...testBrowser('pw-queued'), state: 'completed', recording_state: 'saving' }])
  expect(host.textContent).toContain('Preparing video replay')
  expect(host.textContent).not.toContain('Waiting for the browser’s live view')
})


it('distinguishes repeated fixture names by run and keeps the same name for replay', async () => {
  const first = { ...testBrowser('pw-11111111-first'), label: 'auth_login_gate' }
  const second = { ...testBrowser('pw-22222222-second'), label: 'auth_login_gate' }
  api.get.mockResolvedValue({ data: { sessions: [first, second] } })
  const { selector } = await mountBrowser()
  expect(selector.selectedOptions[0].textContent).toBe('auth_login_gate · 11111111 · Live · Auto')
  expect([...selector.options].map(option => option.textContent)).toContain('auth_login_gate · 22222222 · Live')
  await act(async () => { selector.value = first.browser_session; selector.dispatchEvent(new Event('change', { bubbles: true })) })
  await pollBrowsers([{ ...first, state: 'completed', recording_state: 'saving' }, second])
  expect(selector.value).toBe(first.browser_session)
  expect(selector.selectedOptions[0].textContent).toBe('auth_login_gate · 11111111 · Replay')
  expect(selector.title).toBe('auth_login_gate · 11111111 · Replay')
})

it('allows tall page resizing only after control and maps clicks to the new viewport', async () => {
  api.get.mockResolvedValue({ data: { sessions: [shared] } })
  const { root, host } = await mountBrowser()
  await act(async () => { root.render(<WorkflowLiveBrowser workspacePath="Workflow/test" minimal />) })
  const size = host.querySelector('select[aria-label="Browser page size"]') as HTMLSelectElement
  expect(size.disabled).toBe(true)
  const ws = FakeSocket.instances.at(-1)!
  await act(async () => { ws.onmessage?.({data: JSON.stringify({type:'viewer_control', controlling:true})}) })
  expect(size.disabled).toBe(false)
  await act(async () => { size.value='900x1200'; size.dispatchEvent(new Event('change',{bubbles:true})) })
  expect(ws.send).toHaveBeenCalledWith(JSON.stringify({type:'resize_viewport',width:900,height:1200}))
  await act(async () => { ws.onmessage?.({data:JSON.stringify({type:'frame',data:'/9j/',metadata:{deviceWidth:900,deviceHeight:1200}})}) })
  const image = host.querySelector('img')!
  image.getBoundingClientRect = () => ({left:10,top:20,width:450,height:600} as DOMRect)
  await act(async () => { image.dispatchEvent(new MouseEvent('mousedown',{bubbles:true,clientX:235,clientY:320})) })
  expect(JSON.parse(ws.send.mock.calls.at(-1)![0])).toMatchObject({type:'input_mouse',x:450,y:600})
  await act(async () => { ws.onmessage?.({data: JSON.stringify({type:'viewer_control', controlling:false})}) })
  expect((host.querySelector('select[aria-label="Browser page size"]') as HTMLSelectElement).disabled).toBe(true)
})

it('puts the session picker in the header instead of a separate row', async () => {
  api.get.mockResolvedValue({ data: { sessions: [shared, crew] } })
  const { host, selector } = await mountBrowser()
  expect(host.querySelector('header h2')?.textContent).toBe('Browser')
  expect(host.querySelector('header [role="status"]')?.textContent).toBeTruthy()
  // With browsers to pick from, the picker replaces the explanatory subtitle.
  expect(host.textContent).not.toContain('See what your helper does in its browser.')
  expect(host.textContent).not.toContain('streaming-capable')
  expect(host.querySelector('header')!.contains(selector)).toBe(true)
})

const frameMessage = (data = '/9j/') => ({ data: JSON.stringify({ type: 'frame', data, metadata: { deviceWidth: 1280, deviceHeight: 800 } }) })
const buttonNamed = (host: HTMLElement, name: string) => [...host.querySelectorAll('button')].find(button => button.textContent === name || button.getAttribute('aria-label') === name) as HTMLButtonElement | undefined

it('explains the idle state in plain language without a picker or controls', async () => {
  api.get.mockResolvedValue({ data: { sessions: [] } })
  const { host, picker } = await mountBrowser()
  expect(host.textContent).toContain('Start a browser to visit a website or show your helper a task.')
  expect(picker()).toBeNull()
  expect(buttonNamed(host, 'Take control')).toBeUndefined()
  expect(buttonNamed(host, 'Reconnect')).toBeUndefined()
  expect(FakeSocket.instances).toHaveLength(0)
})

it('shows a starting message, then header, tabs and one address bar', async () => {
  api.get.mockResolvedValue({ data: { sessions: [shared] } })
  const { host } = await mountBrowser()
  expect(host.textContent).toContain('Starting browser…')
  expect(host.textContent).not.toContain('streaming-capable')
  const ws = FakeSocket.instances.at(-1)!
  await act(async () => {
    ws.onmessage?.(frameMessage())
    ws.onmessage?.({ data: JSON.stringify({ type: 'tabs', tabs: [{ tabId: 't1', title: 'Pricing', url: 'https://example.com/pricing', active: true }] }) })
  })
  const bar = host.querySelector('.live-browser-bar')!
  expect(bar).not.toBeNull()
  expect(host.querySelector('header')).toBe(bar)
  expect(host.textContent).not.toContain('See what your helper does')
  expect(host.querySelector('[role="status"]')?.textContent).toBe('Live')
  expect(bar.textContent).not.toContain('https://example.com/pricing')
  expect(host.querySelector('[role="tab"]')?.textContent).toBe('Pricing')
  expect((host.querySelector('[aria-label="Website address"]') as HTMLInputElement).value).toBe('https://example.com/pricing')
  expect(host.querySelector('[aria-label="Browser tabs"]')).not.toBeNull()
  expect(buttonNamed(host, 'Take control')).toBeDefined()
})

it('reconnects automatically with backoff after the browser restarts', async () => {
  expect(browserReconnectDelayMs(0)).toBe(1000)
  expect(browserReconnectDelayMs(1)).toBe(2000)
  expect(browserReconnectDelayMs(10)).toBe(15000)
  api.get.mockResolvedValue({ data: { sessions: [shared] } })
  const { host } = await mountBrowser(true)
  const first = FakeSocket.instances.at(-1)!
  await act(async () => { first.onmessage?.(frameMessage()) })
  await act(async () => { first.onclose?.() })
  expect(host.textContent).toContain('Browser restarted — reconnecting…')
  expect(buttonNamed(host, 'Reconnect')).toBeUndefined()
  expect(buttonNamed(host, 'Try again')).toBeUndefined()
  await act(async () => { vi.advanceTimersByTime(999) })
  expect(FakeSocket.instances.at(-1)).toBe(first)
  await act(async () => { vi.advanceTimersByTime(1) })
  const second = FakeSocket.instances.at(-1)!
  expect(second).not.toBe(first)
  expect(String(second.url)).toContain('/shared-browser/stream')
  await act(async () => { second.onmessage?.(frameMessage('/9j/back')) })
  expect(host.querySelector('img')?.getAttribute('src')).toBe('data:image/jpeg;base64,/9j/back')
  expect(host.textContent).not.toContain('reconnecting')
})

it('gives up after repeated failures with one friendly line and a single Try again', async () => {
  const warn = vi.spyOn(console, 'warn').mockImplementation(() => {})
  api.get.mockResolvedValue({ data: { sessions: [shared] } })
  const { host } = await mountBrowser(true)
  for (let attempt = 0; attempt < BROWSER_RECONNECT_ATTEMPTS; attempt++) {
    await act(async () => { FakeSocket.instances.at(-1)!.onclose?.() })
    await act(async () => { vi.advanceTimersByTime(15000) })
  }
  expect(FakeSocket.instances).toHaveLength(BROWSER_RECONNECT_ATTEMPTS + 1)
  await act(async () => { FakeSocket.instances.at(-1)!.onclose?.() })
  expect(host.textContent).toContain('We couldn’t reconnect to the browser.')
  expect([...host.querySelectorAll('button')].filter(button => button.textContent === 'Try again')).toHaveLength(1)
  expect(host.querySelector('details summary')?.textContent).toBe('Details')
  expect(warn).toHaveBeenCalled()
  await act(async () => { vi.advanceTimersByTime(60000) })
  expect(FakeSocket.instances).toHaveLength(BROWSER_RECONNECT_ATTEMPTS + 1)
  await act(async () => { buttonNamed(host, 'Try again')!.click() })
  expect(FakeSocket.instances).toHaveLength(BROWSER_RECONNECT_ATTEMPTS + 2)
  expect(host.textContent).toContain('Starting browser…')
  warn.mockRestore()
})

it('hides the browser picker for one browser and shows it for several', async () => {
  api.get.mockResolvedValue({ data: { sessions: [shared] } })
  const { host, picker } = await mountBrowser()
  expect(picker()).toBeNull()
  await pollBrowsers([shared, crew])
  expect(picker()).not.toBeNull()
  expect([...picker().options].map(option => option.value)).toEqual(['playwright-tests', 'shared-browser', 'crew-browser'])
  await pollBrowsers([crew])
  expect(picker()).toBeNull()
  expect(host.textContent).toContain('Crew browser')
})

it('keeps recording and page size in an overflow menu that closes on Escape', async () => {
  api.get.mockResolvedValue({ data: { sessions: [shared] } })
  const { host } = await mountBrowser()
  const ws = FakeSocket.instances.at(-1)!
  await act(async () => { ws.onmessage?.(frameMessage()) })
  expect(buttonNamed(host, 'Start recording')).toBeUndefined()
  const more = buttonNamed(host, 'More browser options')!
  expect(more.getAttribute('aria-haspopup')).toBe('menu')
  expect(more.getAttribute('aria-expanded')).toBe('false')
  await act(async () => { more.click() })
  expect(more.getAttribute('aria-expanded')).toBe('true')
  const items = [...host.querySelectorAll('[role="menu"] [role="menuitem"]')] as HTMLButtonElement[]
  expect(items.map(item => item.textContent)).toEqual(['Start recording', 'Tall page · 900 × 1200', 'Wide page · 1280 × 800'])
  expect(items[1].disabled).toBe(true)
  await act(async () => { document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape' })) })
  expect(host.querySelector('[role="menu"]')).toBeNull()
  await act(async () => { ws.onmessage?.({ data: JSON.stringify({ type: 'viewer_control', controlling: true }) }) })
  expect(buttonNamed(host, 'Give back to helper')?.getAttribute('aria-pressed')).toBe('true')
  await act(async () => { more.click() })
  const tall = [...host.querySelectorAll('[role="menuitem"]')].find(item => item.textContent?.startsWith('Tall')) as HTMLButtonElement
  expect(tall.disabled).toBe(false)
  await act(async () => { tall.click() })
  expect(ws.send).toHaveBeenCalledWith(JSON.stringify({ type: 'resize_viewport', width: 900, height: 1200 }))
  expect(host.querySelector('[role="menu"]')).toBeNull()
  await act(async () => { buttonNamed(host, 'Give back to helper')!.click() })
  expect(ws.send).toHaveBeenLastCalledWith(JSON.stringify({ type: 'release_control' }))
})

it('expands the live view and exits on Escape unless the user is in control', async () => {
  api.get.mockResolvedValue({ data: { sessions: [shared] } })
  const { host } = await mountBrowser()
  expect(buttonNamed(host, 'Expand browser')).toBeUndefined()
  const ws = FakeSocket.instances.at(-1)!
  await act(async () => { ws.onmessage?.(frameMessage()) })
  const section = host.querySelector('section')!
  await act(async () => { buttonNamed(host, 'Expand browser')!.click() })
  expect(section.className).toContain('fixed inset-0')
  expect(buttonNamed(host, 'Exit expanded view')?.getAttribute('aria-pressed')).toBe('true')
  await act(async () => { ws.onmessage?.({ data: JSON.stringify({ type: 'viewer_control', controlling: true }) }) })
  await act(async () => { document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape' })) })
  expect(section.className).toContain('fixed inset-0')
  await act(async () => { ws.onmessage?.({ data: JSON.stringify({ type: 'viewer_control', controlling: false }) }) })
  await act(async () => { document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape' })) })
  expect(section.className).not.toContain('fixed inset-0')
})

it('maps pointer positions through the letterboxed frame', () => {
  // 1000×1000 page painted into a 1000×500 box: scale 0.5, 250px bars left and right.
  const rect = { left: 0, top: 0, width: 1000, height: 500 }
  expect(mapToViewport(500, 250, rect, { width: 1000, height: 1000 })).toEqual({ x: 500, y: 500 })
  expect(mapToViewport(250, 0, rect, { width: 1000, height: 1000 })).toEqual({ x: 0, y: 0 })
  expect(mapToViewport(10, 490, rect, { width: 1000, height: 1000 })).toEqual({ x: 0, y: 980 })
})

it('lets the user start a scoped browser before an agent opens one', async () => {
 api.get.mockResolvedValue({ data: { sessions: [] } })
 const { host } = await mountBrowser()
 api.post.mockResolvedValueOnce({ data: { browser_session: 'workspace-browser' } })
 const start = [...host.querySelectorAll('button')].find(button => button.textContent === 'Start browser')!
 await act(async () => { start.click() })
 expect(api.post).toHaveBeenCalledWith('/api/browser/workspace', { action: 'start' }, expect.objectContaining({ params: { workspace_path: 'Workflow/test', profile_id: undefined } }))
 expect(String(FakeSocket.instances.at(-1)?.url)).toContain('/workspace-browser/stream')
})

it('hides startup for an existing browser and supports per-tab close while teaching', async () => {
  api.get.mockResolvedValue({ data: { sessions: [shared] } })
  const { host } = await mountBrowser()
  expect(buttonNamed(host, 'Start browser')).toBeUndefined()
  const ws = FakeSocket.instances.at(-1)!
  await act(async () => {
    ws.onmessage?.(frameMessage())
    ws.onmessage?.({ data: JSON.stringify({ type: 'viewer_control', controlling: true }) })
    ws.onmessage?.({ data: JSON.stringify({ type: 'tabs', tabs: [{ tabId: 't1', title: 'One', active: true }, { tabId: 't2', title: 'Two', active: false }] }) })
    ws.onmessage?.({ data: JSON.stringify({ type: 'teaching', state: { id: 'demo', status: 'recording', actions: [] } }) })
  })
  expect(host.querySelector('.live-browser-bar')?.textContent).toContain('Teach task')
  expect(buttonNamed(host, 'Start browser')).toBeUndefined()
  await act(async () => { buttonNamed(host, 'New tab')!.click(); buttonNamed(host, 'Close One')!.click(); buttonNamed(host, 'Two')!.click() })
  const sent = ws.send.mock.calls.map(([message]) => JSON.parse(message))
  expect(sent).toContainEqual({ type: 'new_tab', url: 'about:blank' })
  expect(sent).toContainEqual({ type: 'close_tab', tab: 't1' })
  expect(sent).toContainEqual({ type: 'switch_tab', tab: 't2' })
})

it('sends review records to the helper as hidden file context with a plain chat request', async () => {
  api.get.mockResolvedValue({ data: { sessions: [shared] } })
  const { root } = await mountBrowser()
  const onLearn = vi.fn()
  await act(async () => { root.render(<WorkflowLiveBrowser workspacePath="Workflow/test" onLearn={onLearn} />) })
  const ws = FakeSocket.instances.at(-1)!
  await act(async () => { ws.onmessage?.({ data: JSON.stringify({ type: 'teaching', state: { id: 'demo', status: 'draft', goal: 'Export customers', directory: 'Workflow/test/browser-demonstrations/demo' } }) }) })
  const prompt = onLearn.mock.calls[0][0]
  expect(prompt).toContain('browser-demonstrations/demo/manifest.json')
  expect(prompt).toContain('Keep the status draft')
  expect(getDisplaySafeUserMessageContent(prompt)).toBe('Prepare my demonstrated task for reuse: Export customers.')
})

it('keeps startup and tab controls while disabling teaching in SparkQuill', async () => {
  const onLearn = vi.fn()
  api.get.mockResolvedValue({ data: { sessions: [] } })
  const { root, host } = await mountBrowser()
  await act(async () => { root.render(<WorkflowLiveBrowser workspacePath="Chats/SparkQuill" profileId="sparkquill" allowTeaching={false} minimal onLearn={onLearn} />) })
  api.post.mockResolvedValueOnce({ data: { browser_session: 'workspace-browser' } })
  await act(async () => { buttonNamed(host, 'Start browser')!.click() })
  expect(api.post).toHaveBeenCalledWith('/api/browser/workspace', { action: 'start' }, expect.objectContaining({ params: { workspace_path: 'Chats/SparkQuill', profile_id: 'sparkquill' } }))
  const ws = FakeSocket.instances.at(-1)!
  await act(async () => {
    ws.onmessage?.(frameMessage())
    ws.onmessage?.({ data: JSON.stringify({ type: 'viewer_control', controlling: true }) })
    ws.onmessage?.({ data: JSON.stringify({ type: 'tabs', tabs: [{ tabId: 't1', title: 'One', active: true }, { tabId: 't2', title: 'Two', active: false }] }) })
  })
  expect(buttonNamed(host, 'Teach task')).toBeUndefined()
  await act(async () => { ws.onmessage?.({ data: JSON.stringify({ type: 'teaching', state: { status: 'draft', goal: 'Export', directory: 'Chats/SparkQuill/browser-demonstrations/demo' } }) }) })
  expect(onLearn).not.toHaveBeenCalled()
  expect(host.querySelector('[aria-label="Browser page size"]')).not.toBeNull()
  await act(async () => { buttonNamed(host, 'Two')!.click(); buttonNamed(host, 'New tab')!.click(); buttonNamed(host, 'Close One')!.click() })
  expect(ws.send.mock.calls.map(([message]) => JSON.parse(message))).toEqual(expect.arrayContaining([
    { type: 'switch_tab', tab: 't2' }, { type: 'new_tab', url: 'about:blank' }, { type: 'close_tab', tab: 't1' },
  ]))
})

it('transfers native paste and selected copy only while controlling, including Mac shortcuts on Linux', async () => {
  const writeText = vi.fn().mockResolvedValue(undefined)
  const readText = vi.fn().mockResolvedValue('right-click paste\n日本語🙂')
  vi.stubGlobal('navigator', { clipboard: { writeText, readText } })
  api.get.mockResolvedValue({ data: { sessions: [shared] } })
  const { host } = await mountBrowser()
  const ws = FakeSocket.instances.at(-1)!
  await act(async () => { ws.onmessage?.(frameMessage()); ws.onmessage?.({ data: JSON.stringify({ type: 'viewer_control', controlling: true, platform: 'linux' }) }) })
  const input = host.querySelector('[aria-label="Browser keyboard input"]')!
  await act(async () => { input.dispatchEvent(new KeyboardEvent('keydown', { key: 'a', code: 'KeyA', metaKey: true, bubbles: true, cancelable: true })) })
  expect(JSON.parse(ws.send.mock.calls.at(-1)![0])).toMatchObject({ type: 'input_keyboard', key: 'a', modifiers: 2, text: '' })
  ws.send.mockClear()
  const text = 'line one\n日本語🙂'.repeat(150)
  const paste = new Event('paste', { bubbles: true, cancelable: true })
  Object.defineProperty(paste, 'clipboardData', { value: { getData: () => text } })
  await act(async () => { input.dispatchEvent(paste) })
  const chunks = ws.send.mock.calls.map(([value]) => JSON.parse(value))
  expect(chunks.every(message => message.type === 'input_text')).toBe(true)
  expect(chunks.map(message => message.text).join('')).toBe(text)
  expect(chunks.every(message => JSON.stringify(message).length < 16000)).toBe(true)
  expect(paste.defaultPrevented).toBe(true)
  ws.send.mockClear()
  await act(async () => { input.dispatchEvent(new KeyboardEvent('keydown', { key: 'c', metaKey: true, bubbles: true, cancelable: true })) })
  expect(ws.send).toHaveBeenLastCalledWith(JSON.stringify({ type: 'clipboard_copy', requestId: 'copy-1' }))
  await act(async () => { ws.onmessage?.({ data: JSON.stringify({ type: 'clipboard', requestId: 'copy-1', text: 'selected text' }) }) })
  expect(writeText).toHaveBeenCalledWith('selected text')
  // Native macOS Copy menus require a harmless local selection to emit copy.
  expect((input as HTMLTextAreaElement).value).toBe(' ')
  expect((input as HTMLTextAreaElement).selectionEnd).toBe(1)
  await act(async () => { input.dispatchEvent(new Event('copy', { bubbles: true, cancelable: true })) })
  expect(ws.send).toHaveBeenLastCalledWith(JSON.stringify({ type: 'clipboard_copy', requestId: 'copy-2' }))
  await act(async () => { ws.onmessage?.({ data: JSON.stringify({ type: 'clipboard', requestId: 'copy-2', error: 'Select some text before copying.' }) }) })
  expect(writeText).toHaveBeenCalledTimes(1)
  const image = host.querySelector('img')!
  await act(async () => { image.dispatchEvent(new MouseEvent('contextmenu', { bubbles: true, cancelable: true, clientX: 40, clientY: 30 })) })
  expect(host.querySelector('[role="menu"]')).not.toBeNull()
  await act(async () => { image.dispatchEvent(new MouseEvent('mousemove', { bubbles: true, clientX: 42, clientY: 30 })) })
  expect(host.querySelector('[role="menu"]')).not.toBeNull()
  ws.send.mockClear()
  await act(async () => { buttonNamed(host, 'Paste')!.click() })
  expect(ws.send).toHaveBeenLastCalledWith(JSON.stringify({ type: 'input_text', text: 'right-click paste\n日本語🙂' }))
  expect(host.querySelector('[role="menu"]')).toBeNull()
  await act(async () => { ws.onmessage?.({ data: JSON.stringify({ type: 'viewer_control', controlling: false }) }) })
  ws.send.mockClear()
  await act(async () => { input.dispatchEvent(paste); input.dispatchEvent(new KeyboardEvent('keydown', { key: 'c', ctrlKey: true, bubbles: true })) })
  expect(ws.send).not.toHaveBeenCalled()
})

it('keeps tab controls by the tabs and navigation mutations behind manual control', async () => {
  api.get.mockResolvedValue({ data: { sessions: [shared] } })
  const { host } = await mountBrowser()
  const ws = FakeSocket.instances.at(-1)!
  await act(async () => { ws.onmessage?.(frameMessage()); ws.onmessage?.({ data: JSON.stringify({ type: 'tabs', tabs: [{ tabId: 't1', title: 'Course Designer', url: 'https://example.com', active: true }, { tabId: 't2', title: 'Preview', url: 'https://example.com/preview', active: false }] }) }) })
  expect(buttonNamed(host, 'New tab')?.disabled).toBe(true)
  expect(buttonNamed(host, 'Go back')?.disabled).toBe(true)
  expect(buttonNamed(host, 'Open site')).toBeUndefined()
  expect(buttonNamed(host, 'Close tab')).toBeUndefined()
  expect(buttonNamed(host, 'Give back to helper')).toBeUndefined()
  await act(async () => { ws.onmessage?.({ data: JSON.stringify({ type: 'viewer_control', controlling: true }) }) })
  expect(buttonNamed(host, 'Teach task')?.closest('header')).not.toBeNull()
  expect(buttonNamed(host, 'Give back to helper')?.className).not.toMatch(/amber|bg-primary/)
  await act(async () => { buttonNamed(host, 'Go back')!.click(); buttonNamed(host, 'Go forward')!.click(); buttonNamed(host, 'Reload page')!.click() })
  expect(ws.send.mock.calls.map(([value]) => JSON.parse(value))).toEqual(expect.arrayContaining([{ type: 'history', action: 'back' }, { type: 'history', action: 'forward' }, { type: 'history', action: 'reload' }]))
})

it('recovers a previously controlled managed browser once and never restarts teaching', async () => {
  const managed = { browser_session: 'project-0123456789abcdef--browser', workflow_session: 'workspace', label: 'Code browser' }
  api.get.mockResolvedValue({ data: { sessions: [managed] } })
  const { host } = await mountBrowser(true)
  const first = FakeSocket.instances.at(-1)!
  await act(async () => {
    first.onmessage?.(frameMessage())
    first.onmessage?.({ data: JSON.stringify({ type: 'viewer_control', controlling: true }) })
    first.onmessage?.({ data: JSON.stringify({ type: 'teaching', state: { status: 'recording', goal: 'A task' } }) })
    first.onclose?.()
    vi.advanceTimersByTime(1000)
  })
  const second = FakeSocket.instances.at(-1)!
  api.post.mockResolvedValue({ data: { browser_session: managed.browser_session } })
  await act(async () => { second.onclose?.(); vi.advanceTimersByTime(2000) })
  const recoveries = () => api.post.mock.calls.filter(call => call[0] === '/api/browser/workspace' && call[1]?.action === 'recover')
  expect(recoveries()).toHaveLength(1)
  expect(recoveries()[0][2].params.workspace_path).toBe('Workflow/test')
  await act(async () => { FakeSocket.instances.at(-1)!.onclose?.(); vi.advanceTimersByTime(15000) })
  expect(recoveries()).toHaveLength(1)
  expect(FakeSocket.instances.flatMap(socket => socket.send.mock.calls).some(([value]) => JSON.parse(value).type === 'teach_start')).toBe(false)
  expect(host.textContent).not.toContain('Recording your task')
})

it('never launches a missing managed browser for a passive viewer', async () => {
  api.get.mockResolvedValue({ data: { sessions: [{ browser_session: 'project-0123456789abcdef--browser', workflow_session: 'workspace' }] } })
  await mountBrowser(true)
  await act(async () => { FakeSocket.instances.at(-1)!.onmessage?.(frameMessage()); FakeSocket.instances.at(-1)!.onclose?.(); vi.advanceTimersByTime(1000) })
  await act(async () => { FakeSocket.instances.at(-1)!.onclose?.(); vi.advanceTimersByTime(2000) })
  expect(api.post.mock.calls.some(call => call[0] === '/api/browser/workspace')).toBe(false)
})


it('opens the built-in browser directly from the panel, with a retry and no settings detour', async () => {
  vi.useFakeTimers({ toFake: ['setInterval', 'clearInterval'] })
  vi.stubGlobal('IS_REACT_ACT_ENVIRONMENT', true)
  vi.stubGlobal('WebSocket', FakeSocket)
  api.get.mockImplementation(async (url: string) => ({ data: url === '/api/browser/extension'
    ? { selected: false, connected: false, account_connected: false, tabs: 0 }
    : { sessions: [] } }))
  const host = document.createElement('div'); document.body.append(host)
  const root = createRoot(host)
  cleanups.push(() => { act(() => root.unmount()); host.remove() })
  await act(async () => root.render(<TooltipProvider><BrowserWorkspacePanel workspacePath="Chats/Work/projects/one" profileId="work" scopeNoun="project" browserMode="headless" onBrowserModeChange={vi.fn()} cdpPort={9222} onCdpPortChange={vi.fn()} cdpConnected={null} cdpError={null} cdpChecking={false} onCheckCdpConnection={vi.fn()} /></TooltipProvider>))
  expect(host.textContent).toContain('Your browser will appear here')
  expect(host.textContent).toContain('runs a browser test')
  expect(host.textContent).toContain('Requires the AgentWorks browser extension')
  expect(host.textContent).not.toContain('Choose a browser')
  expect(host.textContent).not.toContain('Idle')
  expect(buttonNamed(host, 'Start browser')).toBeUndefined()
  expect([...host.querySelectorAll('button')].filter(button => button.textContent === 'Open browser')).toHaveLength(1)
  api.post.mockRejectedValueOnce(new Error('Browser unavailable'))
  await act(async () => { buttonNamed(host, 'Open browser')!.click() })
  expect(host.querySelector('[role="alert"]')?.textContent).toContain('Browser unavailable')
  expect(buttonNamed(host, 'Open browser')?.disabled).toBe(false)
  let opened!: (value: unknown) => void
  api.post.mockImplementationOnce(() => new Promise(resolve => { opened = resolve }))
  await act(async () => { buttonNamed(host, 'Open browser')!.click() })
  expect(buttonNamed(host, 'Opening browser…')?.disabled).toBe(true)
  expect(host.querySelector('[role="dialog"][aria-label="Browser settings"]')).toBeNull()
  expect(api.post).toHaveBeenLastCalledWith('/api/browser/workspace', { action: 'start' }, expect.objectContaining({ params: { workspace_path: 'Chats/Work/projects/one', profile_id: 'work' } }))
  await act(async () => { opened({ data: { browser_session: 'workspace-browser' } }) })
  expect(String(FakeSocket.instances.at(-1)?.url)).toContain('/workspace-browser/stream')
  await act(async () => { FakeSocket.instances.at(-1)?.onmessage?.(frameMessage()) })
  expect(host.querySelector('img')?.getAttribute('src')).toContain('data:image/jpeg;base64,')
  expect(host.textContent).not.toContain('Your browser will appear here')
})
