// @vitest-environment happy-dom
import { act } from 'react'
import { TooltipProvider } from '../ui/tooltip'
import { createRoot } from 'react-dom/client'
import { afterEach, expect, it, vi } from 'vitest'
const get = vi.hoisted(() => vi.fn())
vi.mock('../../services/api', () => ({ default: { get }, getApiBaseUrl: () => '', getAuthToken: () => null }))
vi.mock('./useVaultSetupDraft', () => ({ useVaultSetupDraft: () => ({ open: vi.fn(), error: '' }) }))
import { useVaultMcpConnections } from './useVaultMcpConnections'
import { McpConnectionsPanel } from './McpConnectionsPanel'
Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
const cleanup: Array<() => void> = []
afterEach(() => { cleanup.splice(0).forEach(fn => fn()); vi.clearAllMocks() })
async function mount() {
 const host = document.createElement('div'); document.body.append(host); const root = createRoot(host)
 function Harness() { const model = useVaultMcpConnections(); return <McpConnectionsPanel {...model} view="connected" /> }
 cleanup.push(() => { act(() => root.unmount()); host.remove() })
 await act(async () => root.render(<TooltipProvider><Harness /></TooltipProvider>)); return { host }
}
it('shows authorized MCPs automatically without project opt-in', async () => {
 get.mockResolvedValue({ data: { servers: [{ id: 'c1', label: 'Company Linear', provider: 'linear', status:'authentication_required', tools: [{ name: 'linear__search', description: 'Search issues', input_schema: { type: 'object' } }] }] } })
 const { host } = await mount()
 expect(get).toHaveBeenCalledWith('/api/vault/connections', {params:{}})
 expect(host.textContent).toContain('Company Linear')
 expect(host.textContent).toContain('Sign-in required')
 expect(host.querySelector('input[type="checkbox"]')).toBeNull()
})
it('does not display connections absent from authorized inventory', async () => {
 get.mockResolvedValue({ data: { servers: [] } })
 const { host } = await mount()
 expect(host.textContent).not.toContain('Company Linear')
 expect(host.querySelector('input[type="checkbox"]')).toBeNull()
})
it('reports unavailable Vault without substituting platform connections', async () => {
 get.mockRejectedValue(new Error('offline'))
 const { host } = await mount()
 expect(host.querySelector('[role="alert"]')?.textContent).toContain('Vault is unavailable')
 expect(host.querySelector('input[type="checkbox"]')).toBeNull()
})
