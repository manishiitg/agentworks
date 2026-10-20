// @vitest-environment happy-dom
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
const state = vi.hoisted(() => ({ allowed_products: null as string[] | null, target: (_value: string) => {}, ask: vi.fn(), save: vi.fn() }))
vi.mock('../../stores/useAuthStore', () => ({ useAuthStore: (selector: (s: unknown) => unknown) => selector({ user: { allowed_products: state.allowed_products } }) }))
vi.mock('../../hooks/useWorkspaceViewTarget', () => ({ useWorkspaceViewTarget: (_view: string, apply: (target: string) => void) => { state.target = apply } }))
vi.mock('../../components/workflow/bots/gmailAskAI', () => ({ useGmailInboundUIEnabled: () => false, getGoogleAppsAskAIMessage: () => 'Google help' }))
vi.mock('../../components/workflow/WorkspaceViewHeader', () => ({ WorkspaceViewHeader: ({ title, below, tabs, actions }: any) => <header><h2>{title}</h2>{below}{tabs && <div aria-label={tabs.ariaLabel}>{tabs.options.map((tab: any) => <button key={tab.value} onClick={() => tabs.onChange(tab.value)}>{tab.label}</button>)}</div>}{actions}</header> }))
vi.mock('../../components/workflow/WorkspaceViewActions', () => ({ WorkspaceViewActions: () => null }))
vi.mock('../../components/integrations/ConnectedProjectAccounts', () => ({ ConnectedProjectAccounts: () => null }))
vi.mock('../../components/integrations/SlackAccountsPanel', () => ({ default: () => <div data-content="slack-account">Existing Slack account permissions</div> }))
vi.mock('../../components/integrations/ProductSlackPanel', () => ({ ProductSlackPanel: () => <div data-content="slack">Act in Slack · Talk to this Crew</div> }))
vi.mock('../../components/workflow/ShareDashboardButton', () => ({ ShareDashboardButton: () => <div data-content="sharing">Existing server-gated sharing</div> }))
vi.mock('../../components/integrations/ProjectMcpPanel', () => ({ ProjectMcpPanel: ({ view }: any) => <div data-content="mcp">{view} MCP content</div> }))
vi.mock('../../components/integrations/ProjectSecretsPanel', () => ({ ProjectSecretsPanel: () => <div data-content="secrets">Existing secrets</div> }))
vi.mock('../../components/skills/SkillsManagerPanel', () => ({ default: () => <div data-content="skills">Existing skills</div> }))
vi.mock('../../components/integrations/ProjectVaultPanel', () => ({ ProjectVaultPanel: () => <div data-content="vault">Existing Vault selection</div> }))
vi.mock('../../components/workflow/ProjectKnowledgebasePanel', () => ({ ProjectKnowledgebasePanel: () => <div data-content="brain">Existing Brain settings</div> }))
vi.mock('./WorkIdentityPanel', () => ({ WorkFoldersSection: () => <div data-content="folders">Existing folder access</div> }))
vi.mock('../../components/workflow/WorkflowBotsPanel', () => ({ default: ({ fixedChannel, channelOnly, ownBotOnly }: any) => <div data-content={fixedChannel} data-channel-only={String(channelOnly)} data-own-bot-only={String(ownBotOnly)}>Existing {fixedChannel}</div> }))
vi.mock('../../components/workflow/WorkflowEmailPanel', () => ({ default: () => <div data-content="gmail">Existing Google apps</div> }))
vi.mock('../../components/integrations/ProjectMcpSetupPanel', () => ({ ProjectMcpSetupPanel: () => <div data-content="cli">Existing MCP setup</div> }))
vi.mock('./CrewPublishedAgents', () => ({ CrewPublishedAgents: () => <div data-content="agents">Chats from your computer</div> }))
vi.mock('./McpAppsSection', () => ({ McpAppsSection: () => null }))
vi.mock('./codeLocalFiles', () => ({ useCodeFilesPreference: () => ({ location: 'server' }) }))
vi.mock('../../stores/useChatStore', () => ({ useChatStore: (select: (value: unknown) => unknown) => select({ chatTabs: {} }) }))
import { WorkIntegrationsPanel } from './WorkIntegrationsPanel'
import { CREW_PRODUCT, CODE_PRODUCT, ProjectProductProvider } from './projectProduct'

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
const cleanup: (() => void)[] = []
beforeEach(() => {
  vi.stubEnv('VITE_DEPLOYMENT_MODE', 'server')
  vi.stubGlobal('localStorage', { getItem: () => null, setItem: vi.fn() })
  window.__APP_RUNTIME_CONFIG__ = { enabledProductSurfaces: ['work', 'code', 'knowledgebase', 'mcp-gateway'], gatewayUrl: 'https://example.com' }
  state.allowed_products = null
})
afterEach(() => { cleanup.splice(0).forEach(fn => fn()); delete window.__APP_RUNTIME_CONFIG__; vi.unstubAllGlobals(); vi.unstubAllEnvs(); vi.clearAllMocks() })
async function mount(product = CREW_PRODUCT, enabledPanels?: Set<string>, canEdit = true) {
  const host = document.createElement('div'); document.body.append(host)
  const root = createRoot(host); cleanup.push(() => { act(() => root.unmount()); host.remove() })
  await act(async () => root.render(<ProjectProductProvider value={product}><WorkIntegrationsPanel canEdit={canEdit} workspacePath="Crew/example" projectId="example" projectTitle="Example" projectTemplates={[]} tabId="tab" onAsk={state.ask} onSelectedServersChange={state.save} workflowContextPaths={[]} onWorkflowContextPathsChange={state.save} enabledPanels={enabledPanels} /></ProjectProductProvider>))
  return host
}

it.each([CREW_PRODUCT, CODE_PRODUCT])('opens current connections for origin $profileId and preserves deep links to Secrets and Skills', async product => {
  const host = await mount(product)
  expect(host.querySelector('[data-content="mcp"]')?.textContent).toBe('connected MCP content')
  expect(host.querySelector('[aria-label="Integration sections"]')).toBeNull()
  expect(host.querySelector('nav')?.textContent).toContain('Add connection')
  expect(host.querySelector('[aria-label="MCP servers"]')).toBeNull()
  await act(async () => state.target('secrets')); expect(host.querySelector('[data-content="secrets"]')).not.toBeNull()
  expect(host.querySelector('[data-content="mcp"]')).toBeNull()
  await act(async () => state.target('overview')); expect(host.querySelector('[data-content="mcp"]')).not.toBeNull()
  await act(async () => state.target('secrets'))
  await act(async () => state.target('apps')); expect(host.querySelector('[data-content="mcp"]')).not.toBeNull()
  expect(state.ask).not.toHaveBeenCalled(); expect(state.save).not.toHaveBeenCalled()
})

it('keeps shortcut/search section IDs and only mounts the selected destination', async () => {
  const host = await mount()
  for (const target of ['secrets', 'brain', 'folders', 'slack', 'whatsapp', 'gmail', 'cli', 'available']) {
    await act(async () => state.target(target))
    expect([...host.querySelectorAll('[data-content]')].map(el => el.getAttribute('data-content'))).toEqual(target === 'secrets' ? ['secrets', 'vault'] : [target === 'available' ? 'mcp' : target === 'slack' ? 'slack' : target])
  }
  expect(state.ask).not.toHaveBeenCalled(); expect(state.save).not.toHaveBeenCalled()
})

it('keeps MCP and Skills hidden for a secrets-only project and rejects their targets', async () => {
  state.allowed_products = ['work']
  const host = await mount(CREW_PRODUCT, new Set(['secrets']))
  expect(host.querySelector('[data-content="secrets"]')).not.toBeNull()
  expect(host.querySelector('nav')?.textContent).not.toContain('MCPs')
  expect(host.querySelector('nav')?.textContent).not.toContain('Skills')
  for (const target of ['connected', 'available', 'skills', 'vault', 'brain']) await act(async () => state.target(target))
  expect(host.querySelector('[data-content="secrets"]')).not.toBeNull()
  for (const target of ['mcp', 'skills', 'vault', 'brain']) expect(host.querySelector(`[data-content="${target}"]`)).toBeNull()
})

it.each([CREW_PRODUCT, CODE_PRODUCT])('uses one Slack page for origin $profileId', async product => {
 const host = await mount(product)
 await act(async () => state.target('slack'))
 expect(host.querySelector('[data-content="slack"]')?.textContent).toContain('Act in Slack')
 expect(host.querySelector('[data-content="slack"]')?.textContent).toContain('Talk to this Crew')
 expect(host.querySelector('[data-content="slack-account"]')).toBeNull()
 await act(async () => state.target('overview'))
 expect(host.querySelector('[data-content="slack"]')).toBeNull()
 expect(state.ask).not.toHaveBeenCalled(); expect(state.save).not.toHaveBeenCalled()
})

it('does not expose disabled bot channels through Use & share or direct targets', async () => {
  const host = await mount(CODE_PRODUCT, new Set(['secrets']))
  expect(host.textContent).not.toContain('Slack')
  expect(host.textContent).not.toContain('WhatsApp')
  for (const target of ['slack', 'whatsapp']) await act(async () => state.target(target))
  expect(host.querySelector('[data-content="slack"]')).toBeNull()
  expect(host.querySelector('[data-content="whatsapp"]')).toBeNull()
  expect(host.querySelector('[data-content="secrets"]')).not.toBeNull()
})


it('keeps published agents discoverable in Connections and mounts only the selected list', async () => {
  const host = await mount()
  expect(host.querySelector('[data-content="agents"]')).toBeNull()
  await act(async () => [...host.querySelectorAll('button')].find(button => button.textContent === 'Chats from your computer')!.click())
  expect(host.querySelector('[data-content="agents"]')).not.toBeNull()
  expect(host.querySelector('[data-content="mcp"]')).toBeNull()
  await act(async () => state.target('overview'))
  expect(host.querySelector('[data-content="agents"]')).toBeNull()
  expect(host.querySelector('[data-content="mcp"]')).not.toBeNull()
  expect(state.ask).not.toHaveBeenCalled(); expect(state.save).not.toHaveBeenCalled()
})

it('does not expose private published agents to a viewer or through a direct section target', async () => {
  const host = await mount(CREW_PRODUCT, undefined, false)
  expect(host.textContent).not.toContain('Chats from your computer')
  await act(async () => state.target('agents'))
  expect(host.querySelector('[data-content="agents"]')).toBeNull()
})
