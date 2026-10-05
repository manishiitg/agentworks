// @vitest-environment happy-dom
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { TooltipProvider } from '../ui/tooltip'

const mocks = vi.hoisted(() => ({ get: vi.fn(), refresh: vi.fn(), remove: vi.fn() }))
vi.mock('../../services/api', () => ({ default: { get: mocks.get } }))
vi.mock('./usePlaceMcpConnections', () => ({ usePlaceMcpConnections: () => ({
  servers: [{ id: 'place-1', name: 'Z Project Notion', status: 'Connected', actions: [{ label: 'Remove project connection', run: mocks.remove }] }],
  loading: false, refresh: mocks.refresh, notices: [],
  catalog: [{ id: 'slack', name: 'Slack' }],
}) }))
import { ProjectMcpPanel } from './ProjectMcpPanel'

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
let cleanup = () => {}
beforeEach(() => {
  vi.clearAllMocks()
  mocks.get.mockResolvedValue({ data: { servers: [{ id: 'vault-1', label: 'A Shared Linear', provider: 'linear', tools: [] }] } })
})
afterEach(() => cleanup())
async function mount(view: 'connected' | 'available' = 'connected') {
  const host = document.createElement('div'); document.body.append(host)
  const root = createRoot(host)
  cleanup = () => { act(() => root.unmount()); host.remove() }
  await act(async () => root.render(<TooltipProvider><ProjectMcpPanel view={view} workspacePath="Workflow/demo"
    placeNoun="workflow" canEdit selectedServers={[]} onSelectedServersChange={() => {}} /></TooltipProvider>))
  return host
}
it('shows project MCPs before Vault with one shared search and no Vault opt-in or project actions', async () => {
  const host = await mount()
  const project = host.querySelector('section[aria-label="Workflow MCPs"]')!
  const vault = host.querySelector('section[aria-label="Vault MCPs"]')!
  expect(project.textContent).toContain('Z Project Notion')
  expect(vault.textContent).toContain('A Shared Linear')
  expect(project.compareDocumentPosition(vault) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy()
  expect(host.querySelectorAll('[aria-label="Search servers"]')).toHaveLength(1)
  expect(vault.querySelector('input[type="checkbox"]')).toBeNull()
  expect(vault.querySelector('[aria-label^="Actions"]')).toBeNull()
  expect(vault.textContent).toContain('Available automatically')
})
it('keeps project connections usable when Vault fails', async () => {
  mocks.get.mockRejectedValue(new Error('Vault offline'))
  const host = await mount()
  expect(host.querySelector('section[aria-label="Workflow MCPs"]')?.textContent).toContain('Z Project Notion')
  expect(host.querySelector('section[aria-label="Vault MCPs"]')?.textContent).not.toContain('A Shared Linear')
  expect(host.querySelector('[role="alert"]')?.textContent).toContain('Vault is unavailable')
})
it('refreshes both sources together', async () => {
  const host = await mount()
  await act(async () => { host.querySelector<HTMLButtonElement>('[aria-label="Refresh MCPs"]')!.click() })
  expect(mocks.refresh).toHaveBeenCalledOnce()
  expect(mocks.get).toHaveBeenCalledTimes(2)
})
it('keeps the Available view for installing connections', async () => {
  const host = await mount('available')
  expect(host.querySelector('section[aria-label="Available servers"]')?.textContent).toContain('Slack')
  expect(host.querySelector('section[aria-label="Vault MCPs"]')).toBeNull()
})
