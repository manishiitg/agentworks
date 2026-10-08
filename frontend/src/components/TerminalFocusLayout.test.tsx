// @vitest-environment happy-dom
import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'

const store = vi.hoisted(() => ({ activeTabId: 'tab', chatTabs: { tab: { sessionId: 'chat-1', viewMode: 'formatted', metadata: { agentProfileProjectId: 'code-1' } } } as Record<string, unknown> }))
vi.mock('../stores/useChatStore', () => ({
  normalizeEventViewMode: (mode: string) => mode,
  useChatStore: Object.assign((selector: (state: typeof store) => unknown) => selector(store), { getState: () => store }),
}))
vi.mock('../stores/useAuthStore', () => ({ useAuthStore: Object.assign((selector: (state: unknown) => unknown) => selector({ user: { id: 'alice' } }), { getState: () => ({ user: { id: 'alice' } }) }) }))
vi.mock('../stores/useWorkspaceConnectionStore', () => ({ useWorkspaceConnectionStore: Object.assign((selector: (state: unknown) => unknown) => selector({ activeWorkspaceId: 'hosted' }), { getState: () => ({ activeWorkspaceId: 'hosted' }) }) }))
vi.mock('../services/api', () => ({ default: {}, getApiBaseUrl: () => 'https://code.example.test' }))
import { TerminalFocusLayout } from './TerminalFocusLayout'
import { setProjectLocalFiles } from '../products/work/codeLocalFiles'

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
let root: Root | undefined
beforeEach(() => {
  const storage = new Map<string, string>()
  vi.stubGlobal('localStorage', { getItem: (key: string) => storage.get(key) ?? null, setItem: (key: string, value: string) => { storage.set(key, value) }, removeItem: (key: string) => { storage.delete(key) }, clear: () => storage.clear() })
})
afterEach(() => { act(() => root?.unmount()); root = undefined; document.body.innerHTML = '' })

async function render(tabId?: string) {
  const host = document.createElement('div'); document.body.append(host)
  root = createRoot(host)
  await act(async () => root?.render(<TerminalFocusLayout tabId={tabId} className="x"><p>chat</p></TerminalFocusLayout>))
  return host
}
const press = async (label: string) => { await act(async () => [...document.body.querySelectorAll('button')].find(b => b.textContent === label)!.click()) }

it('offers focus mode once in a Local workspace, enters it, and shows a way out', async () => {
  setProjectLocalFiles('code-1', { device_id: 'laptop', resource_id: 'app' })
  const host = await render('tab')
  expect(document.body.textContent).toContain('Use focus mode?')
  await press('Enter focus mode')
  expect(host.querySelector('[data-terminal-focus="true"]')).not.toBeNull()
  expect(host.querySelector('[aria-label="Exit focus mode"]')).not.toBeNull()
  await press('Exit focus mode')
  expect(host.querySelector('[data-terminal-focus="true"]')).toBeNull()
  expect(document.body.textContent).not.toContain('Use focus mode?')
})

it('does not ask on server files, again after an answer, or from the app-level layout', async () => {
  setProjectLocalFiles('code-1', null)
  await render('tab')
  expect(document.body.textContent).not.toContain('Use focus mode?')
  act(() => root?.unmount()); root = undefined
  setProjectLocalFiles('code-1', { device_id: 'laptop', resource_id: 'app' })
  await render()
  expect(document.body.textContent).not.toContain('Use focus mode?')
  act(() => root?.unmount()); root = undefined
  await render('tab')
  await press('Not now')
  act(() => root?.unmount()); root = undefined
  await render('tab')
  expect(document.body.textContent).not.toContain('Use focus mode?')
})
