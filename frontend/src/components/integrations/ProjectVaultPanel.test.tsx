// @vitest-environment happy-dom
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { afterEach, expect, it, vi } from 'vitest'
const mocks = vi.hoisted(() => ({ load: vi.fn(), select: vi.fn(), draft: vi.fn(), streaming: false }))
vi.mock('../../api/projectVault', () => ({ loadProjectVault: mocks.load, selectProjectVaultItem: mocks.select }))
vi.mock('./useVaultSetupDraft', () => ({ useVaultSetupDraft: (tabId: string) => ({ open: (message: string) => mocks.draft(tabId, message), error: '' }) }))
vi.mock('../../hooks/useLiveRefetch', () => ({ useLiveRefetch: () => {} }))
vi.mock('../../stores/useChatStore', () => ({ useChatStore: (select: any) => select({ chatTabs: { visible: { isStreaming: mocks.streaming } } }) }))
vi.mock('../../stores/useAuthStore', () => ({ useAuthStore: Object.assign((select: any) => select({ user: { id: 'owner' } }), { getState: () => ({ user: { id: 'owner' } }) }) }))
vi.mock('../../stores/useWorkspaceConnectionStore', () => ({ useWorkspaceConnectionStore: Object.assign((select: any) => select({ activeWorkspaceId: 'server' }), { getState: () => ({ activeWorkspaceId: 'server' }) }) }))
import { ProjectVaultPanel } from './ProjectVaultPanel'
Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
const cleanup: Array<() => void> = []
afterEach(() => { cleanup.splice(0).forEach(fn => fn()); vi.clearAllMocks(); mocks.streaming = false })
const app = { id: 'slack', name: 'Team chat', kind: 'slack', provider: 'Slack', status: 'needs_attention', selected: false, can_select: true }
const inventory = { notices: [], vaults: [
  { id: 'mine', name: 'My accounts', sharing: 'Only you', project: false, items: [app, { id: 'laptop', name: 'My laptop', kind: 'computer', status: 'offline', selected: true, can_select: true }] },
  { id: 'project', name: 'Project vault', sharing: 'You + 1 person', project: true, items: [{ id: 'KEY', name: 'KEY', kind: 'project_secret', status: 'stored', selected: true, can_select: true }] },
] }
async function mount() {
  mocks.load.mockResolvedValue(inventory)
  mocks.select.mockResolvedValue(inventory)
  const host = document.createElement('div'); document.body.append(host)
  const root = createRoot(host); cleanup.push(() => { act(() => root.unmount()); host.remove() })
  const update = async () => { await act(async () => root.render(<ProjectVaultPanel workspacePath="Crew/example" tabId="visible" />)) }
  await update()
  return { host, update }
}
it('shows names, sharing and connection health with project vaults folded and no setup forms', async () => {
  const { host } = await mount()
  expect([...host.querySelectorAll('details')].map(el => el.open)).toEqual([true, false])
  expect(host.textContent).toContain('You + 1 person')
  expect(host.querySelector('[aria-label="Needs attention"]')).not.toBeNull()
  expect(host.querySelector('[aria-label="Offline"]')).not.toBeNull()
  expect(host.textContent).toContain('KEY')
  expect(host.querySelector('form, input:not([type="checkbox"]), textarea, select')).toBeNull()
})
it('uses the server checkbox operation and drafts repair into this exact visible chat', async () => {
  const { host } = await mount()
  await act(async () => host.querySelector<HTMLButtonElement>('[aria-label="Use Team chat from My accounts"]')!.click())
  expect(mocks.select).toHaveBeenCalledWith('Crew/example', app)
  await act(async () => [...host.querySelectorAll('button')].find(button => button.textContent === 'Team chat')!.click())
  expect(mocks.draft).toHaveBeenCalledWith('visible', expect.stringContaining('Reconnect Slack'))
})
it('refreshes after a chat turn finishes, without polling during the turn', async () => {
  const { update } = await mount()
  expect(mocks.load).toHaveBeenCalledTimes(1)
  mocks.streaming = true; await update()
  expect(mocks.load).toHaveBeenCalledTimes(1)
  mocks.streaming = false; await update()
  expect(mocks.load).toHaveBeenCalledTimes(2)
})
