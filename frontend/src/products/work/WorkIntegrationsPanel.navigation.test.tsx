// @vitest-environment happy-dom
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
const state = vi.hoisted(() => ({ allowed_products: null as string[] | null, target: (_value: string) => {}, ask: vi.fn(), save: vi.fn() }))
vi.mock('../../stores/useAuthStore', () => ({ useAuthStore: (selector: (s: unknown) => unknown) => selector({ user: { allowed_products: state.allowed_products } }) }))
vi.mock('../../hooks/useWorkspaceViewTarget', () => ({ useWorkspaceViewTarget: (_view: string, apply: (target: string) => void) => { state.target = apply } }))
vi.mock('../../components/workflow/WorkspaceViewHeader', () => ({ WorkspaceViewHeader: ({ title, below, tabs, actions }: any) => <header><h2>{title}</h2>{below}{tabs && <div aria-label={tabs.ariaLabel}>{tabs.options.map((tab: any) => <button key={tab.value} onClick={() => tabs.onChange(tab.value)}>{tab.label}</button>)}</div>}{actions}</header> }))
vi.mock('../../components/workflow/WorkspaceViewActions', () => ({ WorkspaceViewActions: () => null }))
vi.mock('../../components/integrations/SlackAccountsPanel', () => ({ default: () => <div data-content="slack-account">Existing Slack account permissions</div> }))
vi.mock('../../components/integrations/ProductSlackPanel', () => ({ ProductSlackPanel: ({ section }: any) => <div data-content="slack" data-section={section}>Act in Slack · Talk to this Crew</div> }))
vi.mock('../../components/workspace/ProjectAgentBridgePanel', () => ({ ProjectAgentBridgePanel: ({ target, onAsk }: any) => <button data-content="agentbridge" onClick={() => onAsk(target)}>Contact an agent</button> }))
vi.mock('../../components/workflow/ShareDashboardButton', () => ({ ShareDashboardButton: () => <div data-content="sharing">Existing server-gated sharing</div> }))
vi.mock('../../components/integrations/ProjectMcpPanel', () => ({ ProjectMcpPanel: ({ view }: any) => <div data-content="mcp">{view} MCP content</div> }))
vi.mock('../../components/integrations/ProjectSecretsPanel', () => ({ ProjectSecretsPanel: () => <div data-content="secrets">Existing secrets</div> }))
vi.mock('../../components/skills/SkillsManagerPanel', () => ({ default: () => <div data-content="skills">Existing skills</div> }))
vi.mock('../../components/workflow/ProjectKnowledgebasePanel', () => ({ ProjectKnowledgebasePanel: () => <div data-content="brain">Existing Brain settings</div> }))
vi.mock('./WorkIdentityPanel', () => ({ WorkFoldersSection: () => <div data-content="folders">Existing folder access</div> }))
vi.mock('../../components/workflow/WorkflowBotsPanel', () => ({ default: ({ fixedChannel, channelOnly, ownBotOnly }: any) => <div data-content={fixedChannel} data-channel-only={String(channelOnly)} data-own-bot-only={String(ownBotOnly)}>Existing {fixedChannel}</div> }))
vi.mock('../../components/workflow/WorkflowEmailPanel', () => ({ default: () => <div data-content="gmail">Existing Google apps</div> }))
vi.mock('../../components/integrations/ProjectMcpSetupPanel', () => ({ ProjectMcpSetupPanel: () => <div data-content="cli">Existing MCP setup</div> }))
vi.mock('./ComputerUsePanel', () => ({ ComputerUsePanel: () => <div data-content="computer-use">Local computer</div> }))
vi.mock('./CrewPublishedAgents', () => ({ CrewPublishedAgents: () => <div data-content="agents">Chats from your computer</div> }))
vi.mock('./codeLocalFiles', () => ({ useCodeFilesPreference: () => ({ location: 'server' }), useProjectMode: () => 'dev' }))
vi.mock('../../stores/useChatStore', () => ({ useChatStore: (select: (value: unknown) => unknown) => select({ chatTabs: {} }) }))
vi.mock('../../components/integrations/ProjectIntegrationPanel', () => ({ INTEGRATION_LABELS: {github:'GitHub',connected:'MCPs',available:'Available MCPs',secrets:'Secrets',gmail:'Google apps','slack-account':'Slack account',laptop:'Local laptop'}, ProjectIntegrationPanel: ({section}: any) => <section data-content={section} data-integration-section={section}>Native integration</section> }))
import { WorkIntegrationsPanel } from './WorkIntegrationsPanel'
import { CREW_PRODUCT, ProjectProductProvider } from './projectProduct'

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
const cleanup: (() => void)[] = []
beforeEach(() => {
  vi.stubEnv('VITE_DEPLOYMENT_MODE', 'server')
  vi.stubGlobal('localStorage', { getItem: () => null, setItem: vi.fn() })
  window.__APP_RUNTIME_CONFIG__ = { enabledProductSurfaces: ['work', 'code', 'knowledgebase', 'mcp-gateway', 'bullpen'], gatewayUrl: 'https://example.com' }
  state.allowed_products = null
})
afterEach(() => { cleanup.splice(0).forEach(fn => fn()); delete window.__APP_RUNTIME_CONFIG__; vi.unstubAllGlobals(); vi.unstubAllEnvs(); vi.clearAllMocks() })
async function mount(product = CREW_PRODUCT, enabledPanels?: Set<string>, canEdit = true) {
  const host = document.createElement('div'); document.body.append(host)
  const root = createRoot(host); cleanup.push(() => { act(() => root.unmount()); host.remove() })
  await act(async () => root.render(<ProjectProductProvider value={product}><WorkIntegrationsPanel canEdit={canEdit} workspacePath="Crew/example" projectId="example" projectTitle="Example" tabId="tab" onAsk={state.ask} workflowContextPaths={[]} onWorkflowContextPathsChange={state.save} enabledPanels={enabledPanels} /></ProjectProductProvider>))
  return host
}

it('opens familiar integration panels in the existing project workspace', async () => {
  const host = await mount()
  expect(host.querySelector('header')?.textContent).toContain('MCPs')
  expect(host.querySelector('[data-content="connected"]')).not.toBeNull()
  expect(host.querySelector('nav, [aria-label="Integration sections"]')).toBeNull()
  for (const target of ['brain', 'folders', 'whatsapp', 'cli', 'agents', 'agentbridge', 'connected']) {
    await act(async () => state.target(target))
    expect([...host.querySelectorAll('[data-content]')].map(el => el.getAttribute('data-content'))).toEqual([target])
  }
  expect(state.ask).not.toHaveBeenCalled(); expect(state.save).not.toHaveBeenCalled()
})
it('keeps bot channels and sharing separate from personal Vault accounts', async () => {
  const host = await mount()
  for (const [target, content, section] of [['slack-bot', 'slack', 'bot'], ['share', 'sharing', null]] as const) {
    await act(async () => state.target(target))
    expect(host.querySelector(`[data-content="${content}"]`)?.getAttribute('data-section')).toBe(section)
    expect(host.querySelectorAll('[data-content]')).toHaveLength(1)
  }
  await act(async () => state.target('connected'))
  expect(host.querySelector('[data-content="connected"]')).not.toBeNull()
})
it('retains feature and Builder access checks for direct targets', async () => {
  const host = await mount(CREW_PRODUCT, new Set(['secrets']), false)
  for (const target of ['slack', 'slack-bot', 'whatsapp', 'agents']) await act(async () => state.target(target))
  expect([...host.querySelectorAll('[data-content]')].map(el => el.getAttribute('data-content'))).toEqual(['connected'])
})

it('opens each resource directly without a My Vault destination', async () => {
 const host = await mount()
 for (const target of ['connected','available','secrets','gmail','slack-account','laptop','github']) {
  await act(async () => state.target(target))
  expect(host.querySelector('[data-integration-section]')?.getAttribute('data-integration-section')).toBe(target)
 }
})

// PLAT-856: remote/shared project navigation cannot advertise desktop control.
it('offers Computer use only in a local Builder workspace', async () => {
 const remote = await mount()
 await act(async () => state.target('computer-use'))
 expect(remote.querySelector('[data-content="computer-use"]')).toBeNull()
 vi.stubEnv('VITE_DEPLOYMENT_MODE', 'local')
 const local = await mount()
 await act(async () => state.target('computer-use'))
 expect(local.querySelector('[data-content="computer-use"]')).not.toBeNull()
 const shared = await mount(CREW_PRODUCT, undefined, false)
 await act(async () => state.target('computer-use'))
 expect(shared.querySelector('[data-content="computer-use"]')).toBeNull()
})
