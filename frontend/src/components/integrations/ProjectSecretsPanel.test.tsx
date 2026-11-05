// @vitest-environment happy-dom
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'

const mocks = vi.hoisted(() => ({ globals: vi.fn(), load: vi.fn(), select: vi.fn(), draft: vi.fn() }))
vi.mock('../../api/secrets', () => ({ secretsApi: { getGlobalSecrets: mocks.globals } }))
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
const inventory = { notices: [], selected_servers: [], selected_global_secret_names: [], vaults: [{ id: 'team', name: 'Team vault', sharing: 'Shared with you', items: [...scoped, { id: 'app', name: 'App is separate', kind: 'mcp', selected: false }] }] }
beforeEach(() => {
  vi.clearAllMocks()
  mocks.load.mockResolvedValue(inventory)
  mocks.select.mockResolvedValue({ ...inventory, selected_global_secret_names: [scoped[0].id], vaults: [{ ...inventory.vaults[0], items: inventory.vaults[0].items.map(item => item.id === scoped[0].id ? { ...item, selected: true } : item) }] })
})
afterEach(() => cleanup())

// PLAT-856: a project's secrets are Vault secrets ticked in the Vault picker; values never appear.
it('uses the native Vault inventory for secret ticks', async () => {
  const saved = vi.fn()
  const host = document.createElement('div'); document.body.append(host)
  const root = createRoot(host)
  cleanup = () => { act(() => root.unmount()); host.remove() }
  await act(async () => root.render(<ProjectSecretsPanel workspacePath="Crew/example" onSelectionSaved={saved} />))
  const vault = host.querySelector('[aria-label="Secret list"]')!
  expect(vault.querySelectorAll('[role="checkbox"]')).toHaveLength(18)
  expect(vault.textContent).not.toContain('App is separate')
  expect(mocks.globals).not.toHaveBeenCalled()
  expect(vault.querySelector('[aria-label="Use KEY_17 from Team vault"]')?.hasAttribute('disabled')).toBe(true)
  await act(async () => vault.querySelector<HTMLButtonElement>(`[data-item-id="${scoped[0].id}"] [role="checkbox"]`)!.click())
  expect(mocks.select).toHaveBeenCalledWith('Crew/example', scoped[0])
  expect(saved).toHaveBeenCalledWith(expect.objectContaining({ selected_global_secret_names: [scoped[0].id] }))
})
