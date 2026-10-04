// @vitest-environment happy-dom
import { act, useState } from 'react'
import { TooltipProvider } from '../ui/tooltip'
import { createRoot } from 'react-dom/client'
import { afterEach, expect, it, vi } from 'vitest'
const get = vi.hoisted(() => vi.fn())
vi.mock('../../services/api', () => ({ default: { get } }))
import { useVaultMcpConnections } from './useVaultMcpConnections'
import { McpConnectionsPanel } from './McpConnectionsPanel'
Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
const cleanup: Array<() => void> = []
afterEach(() => { cleanup.splice(0).forEach(fn => fn()); vi.clearAllMocks() })
async function mount(initial: string[] = [], save = vi.fn()) {
 const host = document.createElement('div'); document.body.append(host); const root = createRoot(host)
 function Harness() { const [selected, setSelected] = useState(initial); const model = useVaultMcpConnections({ selectedServers: selected, onSelectedServersChange: async next => { await save(next); setSelected(next) } }); return <McpConnectionsPanel {...model} view="connected" /> }
 cleanup.push(() => { act(() => root.unmount()); host.remove() })
 await act(async () => root.render(<TooltipProvider><Harness /></TooltipProvider>)); return { host, save }
}
it('selects a specific shared connection and preserves other project selections', async () => {
 get.mockResolvedValue({ data: { servers: [{ id: 'c1', label: 'Company Linear', provider: 'linear', tools: [{ name: 'linear__search', description: 'Search issues', input_schema: { type: 'object', required: ['project'] } }] }] } })
 const { host, save } = await mount(['my_private'])
 expect(get).toHaveBeenCalledWith('/api/me/mcp/vault')
 await act(async () => (host.querySelector('input[type="checkbox"]') as HTMLInputElement).click())
 expect(save).toHaveBeenCalledWith(['my_private', 'vault_c1'])
 expect((host.querySelector('input[type="checkbox"]') as HTMLInputElement).checked).toBe(true)
 await act(async () => (host.querySelector('button[aria-expanded]') as HTMLButtonElement).click())
 const args = host.querySelector('summary[aria-label="Arguments for search"]') as HTMLElement
 await act(async () => args.click())
 expect(host.querySelector('pre')?.textContent).toContain('"required"')
 expect(host.querySelector('pre')?.textContent).toContain('"project"')
})
it('allows a revoked selection to be removed without offering unauthorized tools', async () => {
 get.mockResolvedValue({ data: { servers: [] } })
 const { host, save } = await mount(['vault_revoked', 'my_private'])
 expect(host.textContent).toContain('No longer available')
 await act(async () => { host.querySelector<HTMLButtonElement>('[aria-label="Actions for Selected Vault connection"]')!.click() })
 const remove = [...host.querySelectorAll('button')].find(button => button.textContent === 'Remove selection')!
 await act(async () => remove.click())
 expect(save).toHaveBeenCalledWith(['my_private'])
 expect(host.querySelector('input[type="checkbox"]')).toBeNull()
})
it('reports unavailable Vault without substituting platform connections', async () => {
 get.mockRejectedValue(new Error('offline'))
 const { host } = await mount()
 expect(host.querySelector('[role="alert"]')?.textContent).toContain('The connections of this place still work')
 expect(host.querySelector('input[type="checkbox"]')).toBeNull()
})

it('keeps the previous project selection when a save is rejected', async () => {
 get.mockResolvedValue({ data: { servers: [{ id: 'c1', label: 'Company Linear', provider: 'linear', tools: [] }] } })
 const { host, save } = await mount(['my_private'], vi.fn().mockRejectedValue(new Error('permission denied')))
 const selection = host.querySelector<HTMLInputElement>('input[aria-label="Use Company Linear from Vault"]')!
 await act(async () => { selection.click() })
 expect(save).toHaveBeenCalledWith(['my_private', 'vault_c1'])
 expect(selection.checked).toBe(false)
 expect(selection.disabled).toBe(false)
 expect(host.querySelector('[role="alert"]')?.textContent).toContain('Could not save Vault selection')
})
