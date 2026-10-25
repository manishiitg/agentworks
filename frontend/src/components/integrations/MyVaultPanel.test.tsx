// @vitest-environment happy-dom
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { afterEach, expect, it, vi } from 'vitest'

const mocks = vi.hoisted(() => ({ op: vi.fn(), draft: vi.fn(), admin: false }))
vi.mock('../../services/api', () => ({ agentApi: { myVaultsOp: mocks.op } }))
vi.mock('./useVaultSetupDraft', () => ({ useVaultSetupDraft: (tabId: string) => ({ open: (message: string) => mocks.draft(tabId, message), error: '' }) }))
vi.mock('../../stores/useAuthStore', () => ({ useAuthStore: (select: any) => select({ user: { id: 'owner', is_admin: mocks.admin } }) }))
vi.mock('../../stores/useWorkspaceConnectionStore', () => ({ useWorkspaceConnectionStore: (select: any) => select({ activeWorkspaceId: 'server' }) }))
vi.mock('../../stores/useChatStore', () => ({ useChatStore: (select: any) => select({ chatTabs: { visible: { isStreaming: false } } }) }))
vi.mock('./PersonalAccountsPanel', () => ({ default: ({ workspacePath }: { workspacePath?: string }) => <section aria-label="Saved Google and Slack accounts" data-scope={workspacePath || 'personal'}>Google sign-in · Slack sign-in</section> }))
vi.mock('./LaptopAccountsPanel', () => ({ LaptopAccountsPanel: () => <section aria-label="Your computers">Connect your computer</section> }))
vi.mock('./VaultSlackBots', () => ({ VaultSlackBots: () => <section aria-label="Slack bots">Saved Slack bots</section> }))
vi.mock('../admin/SlackAdminPanel', () => ({ default: () => <div>Company bot setup</div> }))
vi.mock('./ProjectVaultPanel', () => ({ ProjectVaultPanel: ({ workspacePath, disabled }: { workspacePath: string; disabled?: boolean }) => <section aria-label="Project selections" data-workspace={workspacePath} data-disabled={disabled}>Project-use ticks</section> }))
import { MyVaultPanel } from './MyVaultPanel'

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
const cleanup: Array<() => void> = []
afterEach(() => { cleanup.splice(0).forEach(fn => fn()); vi.clearAllMocks(); mocks.admin = false })

async function mount(disabled = false) {
  mocks.op.mockResolvedValue(JSON.stringify({ vaults: [{ group: { ID: 'own', Name: 'My accounts' }, role: 'owner', members: ['owner'], secret_names: [], connections: [] }] }))
  const host = document.createElement('div'); document.body.append(host)
  const root = createRoot(host)
  cleanup.push(() => { act(() => root.unmount()); host.remove() })
  await act(async () => root.render(<MyVaultPanel workspacePath="Crew/example" tabId="visible" disabled={disabled} />))
  return host
}

it('keeps full My Vault management beside the current chat, separate from project-use ticks', async () => {
  const host = await mount()
  expect(host.querySelector('[aria-label="This project\'s connections"]')?.compareDocumentPosition(host.querySelector('[aria-label="My accounts, computers and vaults"]')!)).toBe(Node.DOCUMENT_POSITION_FOLLOWING)
  expect((host.querySelector('[aria-label="My accounts, computers and vaults"]') as HTMLDetailsElement).open).toBe(false)
  expect(host.querySelector('[aria-label="Saved Google and Slack accounts"]')?.getAttribute('data-scope')).toBe('personal')
  expect(host.querySelector('[aria-label="Your computers"]')).not.toBeNull()
  expect(host.querySelector('[aria-label="Slack bots"]')).not.toBeNull()
  expect(host.querySelector('[aria-label="Project selections"]')?.getAttribute('data-workspace')).toBe('Crew/example')
  expect(host.textContent).toContain('My accounts') // Empty owned vaults remain manageable.
  expect(host.textContent).not.toContain('Company bot setup')
  await act(async () => [...host.querySelectorAll('button')].find(button => button.textContent?.includes('Connect an app'))!.click())
  expect(mocks.draft).toHaveBeenCalledWith('visible', expect.stringContaining('Keep project selections'))
  await act(async () => [...host.querySelectorAll('button')].find(button => button.textContent?.includes('Manage in chat'))!.click())
  expect(mocks.draft).toHaveBeenLastCalledWith('visible', expect.stringContaining('My accounts'))
})

it('retains administrator controls and withholds personal management when the project cannot be edited', async () => {
  mocks.admin = true
  const admin = await mount()
  expect(admin.textContent).toContain('Company sign-in apps')
  expect(admin.textContent).toContain('Company bot setup')
  const calls = mocks.op.mock.calls.length
  const disabled = await mount(true)
  expect(disabled.querySelector('[aria-label="Saved Google and Slack accounts"]')).toBeNull()
  expect(disabled.textContent).not.toContain('Company bot setup')
  expect(mocks.op).toHaveBeenCalledTimes(calls)
  expect(disabled.querySelector('[aria-label="Project selections"]')?.getAttribute('data-disabled')).toBe('true')
})
