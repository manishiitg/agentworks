// @vitest-environment happy-dom
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { afterEach, expect, it, vi } from 'vitest'
const mocks = vi.hoisted(() => ({ user: { id: 'admin', is_admin: true, is_vault_reader: false }, open: vi.fn(async () => 'project-chat') }))
vi.mock('../../services/api', () => ({ getApiBaseUrl: () => '', getAuthToken: () => null }))
vi.mock('../../components/ModePresetBar', () => ({ ModePresetBar: () => null }))
vi.mock('../../components/GlobalHumanFeedbackPrompt', () => ({ GlobalHumanFeedbackPrompt: () => null }))
vi.mock('../../components/UpdateProgressToast', () => ({ UpdateProgressToast: () => null }))
vi.mock('../../components/topbar/LlmModalHost', () => ({ default: () => null }))
vi.mock('../../components/SchedulesPage', () => ({ default: () => null }))
vi.mock('../../components/AdminPages', () => ({ default: () => null }))
vi.mock('./GatewayServersPanel', () => ({ GatewayServersPanel: () => <p>Admin connections</p> }))
vi.mock('./GatewayWorkspacePane', () => ({ gatewayPanels: [{ id: 'servers', label: 'Connected MCPs' }], GatewayWorkspacePane: ({ servers }: any) => servers }))
vi.mock('./GatewayAuditPanel', () => ({ GatewayAuditPanel: () => null }))
vi.mock('./GatewayConnectPanel', () => ({ GatewayConnectPanel: () => null }))
vi.mock('../../components/workspace/ProductBreadcrumb', () => ({ ProductBreadcrumb: ({ menuItems }: any) => <nav>{menuItems.map((item: any) => <button key={item.id} onClick={item.onSelect}>{item.label}</button>)}</nav> }))
vi.mock('../../utils/projectIntegrationNavigation', () => ({ openLastProjectIntegrationChat: mocks.open }))
vi.mock('../../utils/workspacePaneChat', () => ({ openWorkspacePaneChatWithDraft: vi.fn() }))
vi.mock('../../stores/useAuthStore', () => ({ useAuthStore: (select: any) => select({ user: mocks.user, checkAuth: vi.fn() }) }))
vi.mock('../../stores/useAppStore', () => ({ useAppStore: (select: any) => select({ adminPage: null, showSchedulesOverview: false }) }))
vi.mock('../../stores/useLLMStore', () => ({ useLLMStore: (select: any) => select({ showLLMModal: false }) }))
vi.mock('../../stores/useChatStore', () => ({ useChatStore: { getState: () => ({ addToast: vi.fn() }) } }))
vi.mock('../../hooks/useProductUrlView', () => ({ useProductUrlView: () => {} }))
vi.mock('../../hooks/useWorkspaceViewPreference', () => ({ useWorkspaceViewPreference: (_product: string, _base: string, value: string) => [value, vi.fn()] }))
vi.mock('../../stores/usePanelSwitcherStore', () => ({ useRegisterPanelSwitcher: () => {} }))
vi.mock('../../stores/useWorkspaceConnectionStore', () => ({ useWorkspaceConnectionStore: (select: any) => select({ activeWorkspaceId: 'server' }) }))
import { GatewaySurface } from './GatewaySurface'
Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
const cleanup: Array<() => void> = []
afterEach(() => { cleanup.splice(0).forEach(fn => fn()); vi.clearAllMocks(); delete window.__APP_RUNTIME_CONFIG__; mocks.user.is_admin = true })
async function mount() {
  window.__APP_RUNTIME_CONFIG__ = { gatewayUrl: 'https://vault.example.com' }
  const host = document.createElement('div'); document.body.append(host)
  const root = createRoot(host); cleanup.push(() => { act(() => root.unmount()); host.remove() })
  await act(async () => root.render(<GatewaySurface />))
  return host
}
it('retains Vault administration without creating a separate assistant chat', async () => {
  const host = await mount()
  expect(host.textContent).toContain('Admin connections')
  expect(host.querySelector('textarea')).toBeNull()
  await act(async () => [...host.querySelectorAll('button')].find(button => button.textContent === 'My vaults')!.click())
  expect(mocks.open).toHaveBeenCalledOnce()
})
it('keeps the administrator access boundary when My vaults moves into project chat', async () => {
  mocks.user.is_admin = false
  const host = await mount()
  expect(host.textContent).toContain('Vault needs an administrator or a Vault reader account')
  expect(host.textContent).not.toContain('Admin connections')
})
