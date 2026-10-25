// @vitest-environment happy-dom
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'

const mocks = vi.hoisted(() => ({ project: vi.fn(), globals: vi.fn(), load: vi.fn(), select: vi.fn(), draft: vi.fn() }))
vi.mock('../../api/secrets', () => ({ secretsApi: { listWorkflowSecrets: mocks.project, getGlobalSecrets: mocks.globals } }))
vi.mock('../../api/projectVault', () => ({ loadProjectVault: mocks.load, selectProjectVaultItem: mocks.select }))
vi.mock('../../hooks/useCanWriteWorkflow', () => ({ useCanWriteWorkflow: () => true }))
vi.mock('../../hooks/useLiveRefetch', () => ({ useLiveRefetch: () => {} }))
vi.mock('../secrets/SecretSourceView', () => ({ default: () => null }))
vi.mock('../../utils/workspacePaneChat', () => ({ openWorkspacePaneChatWithDraft: vi.fn() }))
vi.mock('./useVaultSetupDraft', () => ({ useVaultSetupDraft: () => ({ open: mocks.draft, error: '' }) }))
vi.mock('../../stores/useChatStore', () => ({ useChatStore: (select: any) => select({ chatTabs: {} }) }))
vi.mock('../../stores/useAuthStore', () => ({ useAuthStore: Object.assign((select: any) => select({ user: { id: 'owner' } }), { getState: () => ({ user: { id: 'owner' } }) }) }))
vi.mock('../../stores/useWorkspaceConnectionStore', () => ({ useWorkspaceConnectionStore: Object.assign((select: any) => select({ activeWorkspaceId: 'server' }), { getState: () => ({ activeWorkspaceId: 'server' }) }) }))
import { ProjectSecretsPanel } from './ProjectSecretsPanel'

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
let cleanup = () => {}
const scoped = Array.from({ length: 18 }, (_, index) => ({ id: `VLT_team__KEY_${index}`, name: `KEY_${index}`, kind: 'shared_secret', status: 'stored', selected: false, can_select: index !== 17 }))
const inventory = { notices: [], selected_global_secret_names: [], vaults: [{ id: 'team', name: 'Team vault', sharing: 'Shared with you', items: [...scoped, ...['FIRST_GLOBAL', 'SECOND_GLOBAL'].map(name => ({ id: name, name, kind: 'shared_secret', selected: false, can_select: true })), { id: 'app', name: 'App is separate', kind: 'mcp', selected: false }] }] }
beforeEach(() => {
  vi.clearAllMocks()
  mocks.project.mockResolvedValue([{ name: 'PROJECT_KEY', encrypted_value: 'sealed-project-value' }])
  mocks.load.mockResolvedValue(inventory)
  mocks.select.mockResolvedValue({ ...inventory, selected_global_secret_names: [scoped[0].id], vaults: [{ ...inventory.vaults[0], items: inventory.vaults[0].items.map(item => item.id === scoped[0].id ? { ...item, selected: true } : item) }] })
})
afterEach(() => cleanup())
async function mount() {
  const local = vi.fn(), saved = vi.fn()
  const host = document.createElement('div'); document.body.append(host)
  const root = createRoot(host)
  cleanup = () => { act(() => root.unmount()); host.remove() }
  await act(async () => root.render(<ProjectSecretsPanel workspacePath="Crew/example" placeNoun="Crew" selectedSecrets={['PROJECT_KEY']} onSecretChange={local} onSharedSecretsSaved={saved} />))
  return { host, local, saved }
}
it('uses the full native inventory for shared-secret ticks, including scoped Vault names', async () => {
  const { host, local, saved } = await mount()
  const project = host.querySelector('section[aria-label="For this Crew"]')!
  const vault = host.querySelector('section[aria-label="Shared from Vault"]')!
  expect(project.compareDocumentPosition(vault) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy()
  expect(vault.querySelectorAll('[role="checkbox"]')).toHaveLength(20)
  expect(vault.textContent).not.toContain('App is separate')
  expect(mocks.globals).not.toHaveBeenCalled()
  expect(vault.querySelector('[aria-label="Reveal KEY_0"]')).toBeNull()
  expect(vault.querySelector('form, input[type="password"]')).toBeNull()
  expect(host.textContent).not.toContain('sealed-project-value')
  expect(vault.querySelector('[aria-label="Use KEY_17 from Team vault"]')?.hasAttribute('disabled')).toBe(true)
  await act(async () => vault.querySelector<HTMLButtonElement>('[aria-label="Use KEY_0 from Team vault"]')!.click())
  expect(mocks.select).toHaveBeenCalledWith('Crew/example', scoped[0])
  expect(saved).toHaveBeenCalledWith([scoped[0].id])
  expect(local).not.toHaveBeenCalled()
})
it('retains shared Vault names if the project secret store cannot load', async () => {
  mocks.project.mockRejectedValue(new Error('Project secrets unavailable'))
  const { host } = await mount()
  expect(host.querySelector('section[aria-label="For this Crew"] [role="alert"]')?.textContent).toContain('Project secrets unavailable')
  expect(host.querySelector('section[aria-label="Shared from Vault"]')?.textContent).toContain('KEY_0')
  expect(host.querySelector('section[aria-label="Shared from Vault"] [role="alert"]')).toBeNull()
})
