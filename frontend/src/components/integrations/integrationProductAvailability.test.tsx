// @vitest-environment happy-dom
import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { IntegrationSectionPicker } from './IntegrationSectionPicker'
import { projectIntegrationSections } from './integrationSections'
import { ProjectPluginsPanel } from './ProjectPluginsPanel'
import { ProjectKnowledgebasePanel } from '../workflow/ProjectKnowledgebasePanel'
import { knowledgebaseApi } from '../../services/knowledgebaseApi'

const access = vi.hoisted(() => ({ allowed_products: null as string[] | null }))
vi.mock('../../stores/useAuthStore', () => ({ useAuthStore: (selector: (state: { user: typeof access }) => unknown) => selector({ user: access }) }))
vi.mock('../../services/knowledgebaseApi', () => ({ knowledgebaseApi: { bootstrap: vi.fn(), project: vi.fn() }, knowledgebaseError: () => 'Could not load Brain' }))

let root: Root
let host: HTMLDivElement
beforeEach(() => {
  vi.resetAllMocks()
  access.allowed_products = null
  vi.stubEnv('VITE_DEPLOYMENT_MODE', 'server')
  window.__APP_RUNTIME_CONFIG__ = { enabledProductSurfaces: ['work', 'knowledgebase', 'mcp-gateway'], gatewayUrl: 'https://example.com' }
  vi.mocked(knowledgebaseApi.bootstrap).mockResolvedValue({} as never)
  vi.mocked(knowledgebaseApi.project).mockResolvedValue({ manifest_version: 'v1', can_manage: false })
  host = document.createElement('div'); document.body.append(host); root = createRoot(host)
})
afterEach(async () => { await act(async () => root.unmount()); host.remove(); delete window.__APP_RUNTIME_CONFIG__; vi.unstubAllEnvs() })

it.each(['local', 'server', 'account', 'enabled', 'relay'])('keeps product integrations consistent with %s availability, including a saved Vault tab', async scenario => {
  if (scenario === 'local') vi.stubEnv('VITE_DEPLOYMENT_MODE', 'local')
  if (scenario === 'server') window.__APP_RUNTIME_CONFIG__ = { enabledProductSurfaces: ['work'] }
  if (scenario === 'account') access.allowed_products = ['work']
  await act(async () => root.render(<>
    <IntegrationSectionPicker options={projectIntegrationSections(access.allowed_products, scenario === 'relay')} onSelect={() => {}} />
    <ProjectPluginsPanel tab="secrets" connections={view => <p>{view} MCPs</p>} skills={<p>Project skills</p>} vault={<p>Vault groups</p>} />
    {scenario !== 'relay' && <ProjectKnowledgebasePanel workspacePath="Workflow/example" />}
  </>))
  const menu = host.querySelector('[aria-label="Integration sections"]')!
  if (scenario === 'enabled') {
    expect(menu.textContent).toContain('Brain')
    expect(host.querySelector('[role="tabpanel"]')?.textContent).toBe('Vault groups')
    expect(host.querySelector('[aria-label="About Brain"]')).not.toBeNull()
  } else if (scenario === 'relay') {
    expect(menu.querySelectorAll('button')).toHaveLength(4)
    expect(menu.textContent).toContain('WhatsApp')
    expect(menu.textContent).toContain('Google apps')
    expect(menu.textContent).not.toContain('Brain')
    expect(menu.textContent).toContain('Slack')
    expect(host.querySelector('[role="tabpanel"]')?.textContent).toBe('Vault groups')
    expect(knowledgebaseApi.bootstrap).not.toHaveBeenCalled()
  } else {
    expect(menu.textContent).not.toContain('Brain')
    // The project section keeps its Vault name even when the company Vault
    // is unavailable; only the company-backed content is excluded.
    expect(menu.textContent).not.toContain('Vault groups')
    expect(host.querySelector('[role="tabpanel"]')?.textContent).toBe('connected MCPs')
    expect(host.querySelector('[aria-label="About Brain"]')).toBeNull()
    expect(knowledgebaseApi.bootstrap).not.toHaveBeenCalled()
    expect(knowledgebaseApi.project).not.toHaveBeenCalled()
  }
  expect(menu.textContent).toContain('Vault')
})
