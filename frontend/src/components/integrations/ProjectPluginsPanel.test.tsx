// @vitest-environment happy-dom
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { ProjectPluginsPanel } from './ProjectPluginsPanel'
import { TooltipProvider } from '../ui/tooltip'
const get = vi.hoisted(() => vi.fn())
vi.mock('../../services/api', () => ({ default: { get } }))
vi.mock('./OpenVaultButton', () => ({ OpenVaultButton: () => null }))
vi.mock('../../stores/useAuthStore', () => ({ useAuthStore: (selector: (state: { user: null }) => unknown) => selector({ user: null }) }))
Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
let cleanup = () => {}
beforeEach(() => {
  vi.stubEnv('VITE_DEPLOYMENT_MODE', 'server')
  window.__APP_RUNTIME_CONFIG__ = { enabledProductSurfaces: ['work', 'mcp-gateway'], gatewayUrl: 'https://example.com' }
})
afterEach(() => { cleanup(); window.localStorage?.clear(); vi.clearAllMocks(); delete window.__APP_RUNTIME_CONFIG__; vi.unstubAllEnvs() })
const inventory = {
  groups: [{ id: 'eng', name: 'Engineering', description: 'Product team', servers: [{ id: 'n', label: 'Notion · Engineering', provider: 'notion', tools: [] }], secrets: [{ name: 'TEAM_KEY' }] }],
  servers: [{ id: 'n', label: 'Notion · Engineering', provider: 'notion', tools: [] }], secrets: [{ name: 'TEAM_KEY' }],
}
async function mount(view: React.ReactNode) {
  const host = document.createElement('div'); document.body.append(host)
  const root = createRoot(host); cleanup = () => { act(() => root.unmount()); host.remove() }
  await act(async () => { root.render(<TooltipProvider>{view}</TooltipProvider>) })
  return host
}
it('puts Connected MCPs, Available MCPs, Secrets, Skills and Vault in one row', async () => {
  const host = await mount(<ProjectPluginsPanel connections={view => <p>{view} content</p>} secrets={<p>Project secrets</p>} skills={<p>Project skills</p>} vault={<p>My groups</p>}/> )
  expect([...host.querySelectorAll('[role=tab]')].map(tab => tab.textContent)).toEqual(['Connected MCPs','Available MCPs','Vault & secrets','Skills'])
  expect(host.querySelectorAll('[role=tablist]')).toHaveLength(1)
  await act(async () => { host.querySelector<HTMLButtonElement>('[title="Available MCPs"]')!.click() })
  expect(host.textContent).toContain('available content')
  expect(host.textContent).not.toContain('connected content')
  await act(async () => { host.querySelector<HTMLButtonElement>('[title="Vault & secrets"]')!.click() })
  expect(host.textContent).toContain('My groups')
})
it('renders only content when the shared header owns the selected tab', async () => {
  const host = await mount(<ProjectPluginsPanel tab="secrets" connections={view => <p>{view}</p>} secrets={<p>Secrets editor</p>} vault={<p>Authorized groups</p>}/>)
  expect(host.querySelector('[role=tablist]')).toBeNull()
  expect(host.querySelector('[role=tabpanel]')?.getAttribute('aria-label')).toBe('Vault & secrets')
  expect(host.textContent).toContain('Authorized groups')
  expect(host.textContent).toContain('Secrets editor')
})
