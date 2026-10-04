// @vitest-environment happy-dom
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { afterEach, expect, it, vi } from 'vitest'
import { ProjectPluginsPanel } from './ProjectPluginsPanel'
import { ProjectVaultPanel } from './ProjectVaultPanel'
import { TooltipProvider } from '../ui/tooltip'
const get = vi.hoisted(() => vi.fn())
vi.mock('../../services/api', () => ({ default: { get } }))
vi.mock('./OpenVaultButton', () => ({ OpenVaultButton: () => null }))
Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
let cleanup = () => {}
afterEach(() => { cleanup(); window.localStorage?.clear(); vi.clearAllMocks() })
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
it('puts Connected, Available, Secrets, Skills and Vault in one row', async () => {
  const host = await mount(<ProjectPluginsPanel connections={view => <p>{view} content</p>} secrets={<p>Project secrets</p>} skills={<p>Project skills</p>} vault={<p>My groups</p>}/> )
  expect([...host.querySelectorAll('[role=tab]')].map(tab => tab.textContent)).toEqual(['Connected','Available','Secrets','Skills','Vault'])
  expect(host.querySelectorAll('[role=tablist]')).toHaveLength(1)
  await act(async () => { host.querySelector<HTMLButtonElement>('[title=Available]')!.click() })
  expect(host.textContent).toContain('available content')
  expect(host.textContent).not.toContain('connected content')
  await act(async () => { host.querySelector<HTMLButtonElement>('[title=Vault]')!.click() })
  expect(host.textContent).toContain('My groups')
})
it('uses the authenticated inventory, shows groups and selects exact resource references', async () => {
  get.mockResolvedValue({ data: inventory })
  const secrets = vi.fn(async () => {})
  const host = await mount(<ProjectVaultPanel selectedSecrets={[]} onSelectedSecretsChange={secrets}/>)
  expect(get).toHaveBeenCalledWith('/api/me/mcp/vault')
  expect(host.textContent).toContain('Engineering')
  expect(host.textContent).toContain('TEAM_KEY')
  expect(host.textContent).not.toContain('tools')
  expect(host.querySelector('[aria-label="Use Notion · Engineering from Engineering"]')).toBeNull()
  expect(host.textContent).toContain('Available automatically')
  await act(async () => { host.querySelector<HTMLButtonElement>('[aria-label="Use TEAM_KEY from Engineering"]')!.click() })
  expect(secrets).toHaveBeenCalledWith(['TEAM_KEY'])
})
it('clears stale access on refresh and lets a project remove revoked selections', async () => {
  get.mockResolvedValueOnce({ data: inventory }).mockResolvedValueOnce({ data: { groups: [], servers: [], secrets: [] } })
  const secrets = vi.fn(async () => {})
  const host = await mount(<ProjectVaultPanel selectedSecrets={['TEAM_KEY']} onSelectedSecretsChange={secrets}/>)
  await act(async () => { host.querySelector<HTMLButtonElement>('[aria-label="Refresh Vault access"]')!.click() })
  expect(host.textContent).not.toContain('Notion · Engineering')
  expect(host.textContent).toContain('No longer available')
  const remove = [...host.querySelectorAll('button')].filter(button => button.textContent === 'Remove selection')
  await act(async () => { remove[0].click() })
  expect(secrets).toHaveBeenCalledWith([])
})
it('disables all resource selection for a project viewer', async () => {
  get.mockResolvedValue({ data: inventory })
  const host = await mount(<ProjectVaultPanel disabled selectedSecrets={[]} onSelectedSecretsChange={() => {}}/>)
  expect([...host.querySelectorAll<HTMLButtonElement>('[role=checkbox]')].every(checkbox => checkbox.disabled)).toBe(true)
})

it('renders only content when the shared header owns the selected tab', async () => {
  const host = await mount(<ProjectPluginsPanel tab="vault" connections={view => <p>{view}</p>} secrets={<p>Secrets editor</p>} vault={<p>Authorized groups</p>}/>)
  expect(host.querySelector('[role=tablist]')).toBeNull()
  expect(host.querySelector('[role=tabpanel]')?.getAttribute('aria-label')).toBe('Vault')
  expect(host.textContent).toBe('Authorized groups')
})
