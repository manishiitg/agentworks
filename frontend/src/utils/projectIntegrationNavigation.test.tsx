// @vitest-environment happy-dom
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { afterEach, expect, it, vi } from 'vitest'
const mocks = vi.hoisted(() => ({
  state: { chatTabs: {} as Record<string, any>, activeTabId: 'active' },
  presets: { workflowPresetsLoaded: true, workflowPresets: [{ id: 'goal', workflowKind: 'workflow' }], refreshPresets: vi.fn() },
  reveal: vi.fn(), activate: vi.fn((_id: string) => true), draft: vi.fn(), preference: vi.fn(), view: vi.fn(), target: vi.fn(),
}))
vi.mock('../stores/useChatStore', () => ({ useChatStore: { getState: () => mocks.state }, waitForChatStoreHydration: async () => {} }))
vi.mock('../stores/useGlobalPresetStore', () => ({ useGlobalPresetStore: { getState: () => mocks.presets } }))
vi.mock('../stores/useWorkflowStore', () => ({ useWorkflowStore: { getState: () => ({ openWorkspaceView: mocks.view }) } }))
vi.mock('./activateTab', () => ({ activateTab: mocks.activate }))
vi.mock('./productWorkspaceNavigation', () => ({ openProductWorkspace: mocks.reveal }))
vi.mock('./workspaceViewPreference', () => ({ writeWorkspaceViewPreference: mocks.preference }))
vi.mock('../hooks/useWorkspaceViewTarget', () => ({ setWorkspaceViewTarget: mocks.target }))
vi.mock('./workspacePaneChat', () => ({ openWorkspacePaneChatWithDraft: mocks.draft }))
import { isProjectIntegrationChat, openLastProjectIntegrationChat, listenForProjectIntegrationNavigation } from './projectIntegrationNavigation'
import { useVaultSetupDraft } from '../components/integrations/useVaultSetupDraft'
Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
afterEach(() => { vi.clearAllMocks(); mocks.state.chatTabs = {}; mocks.state.activeTabId = 'active'; document.body.innerHTML = '' })
const crew = (id: string, canWrite = true) => ({ tabId: id, createdAt: 1, lastAccessedAt: 2, metadata: { mode: 'multi-agent', agentProfileId: 'work', agentProfileProjectId: 'team', agentProfileTabCanWrite: canWrite } })
it('drafts only into the exact visible owned tab and reveals its product surface', async () => {
  mocks.state.chatTabs = { old: crew('old'), active: crew('active') }
  let setup: ReturnType<typeof useVaultSetupDraft>
  function Harness() { setup = useVaultSetupDraft(); return <p>{setup.error}</p> }
  const host = document.createElement('div'); document.body.append(host); const root = createRoot(host)
  await act(async () => root.render(<Harness />))
  await act(async () => setup!.open('Reconnect my Slack'))
  expect(mocks.reveal).toHaveBeenCalledWith('work')
  expect(mocks.draft).toHaveBeenCalledWith({ tabId: 'active', message: 'Reconnect my Slack' })
  mocks.state.chatTabs.active = crew('active', false)
  await act(async () => setup!.open('Add a secret'))
  expect(mocks.draft).toHaveBeenCalledTimes(1)
  expect(host.textContent).toContain('Open your own Crew or Goal chat')
  await act(async () => root.unmount())
})
it('Integrations opens the last accessible project chat on its MCP panel', async () => {
  mocks.state.chatTabs = { active: { ...crew('active'), metadata: { isViewOnly: true } }, own: crew('own'), other: { ...crew('other', false), lastAccessedAt: 100 } }
  const stop = listenForProjectIntegrationNavigation(vi.fn())
  mocks.activate.mockImplementation((id: string) => { mocks.state.activeTabId = id; return true })
  expect(await openLastProjectIntegrationChat()).toBe('own')
  expect(mocks.activate).toHaveBeenCalledWith('own')
  expect(mocks.target).toHaveBeenCalledWith('mcp', 'connected')
  expect(mocks.preference).toHaveBeenCalledWith('work', 'team', 'mcp')
  stop()
  mocks.state.chatTabs = {}
  await expect(openLastProjectIntegrationChat()).rejects.toThrow('Open a Crew, Goal or Flow chat first')
})
it('keeps execution and scheduled Goal chats out of Vault setup', () => {
  const goal = { ...crew('active'), metadata: { mode: 'workflow', phaseId: 'workflow-builder', presetQueryId: 'goal' } } as any
  expect(isProjectIntegrationChat(goal)).toBe(true)
  expect(isProjectIntegrationChat({ ...goal, metadata: { ...goal.metadata, isScheduledRun: true } })).toBe(false)
})

it('the reply GitHub link reaches Secrets through the real Crew navigation listener', async () => {
  mocks.state.activeTabId = 'active'
  mocks.state.chatTabs = { active: crew('active') }
  const open = vi.fn()
  const stop = listenForProjectIntegrationNavigation(open)
  try {
    await openLastProjectIntegrationChat('secrets')
    expect(open).toHaveBeenCalledOnce()
    expect(mocks.target.mock.calls).toEqual([['mcp', 'secrets']])
    expect(mocks.activate).toHaveBeenCalledWith('active')
  } finally { stop() }
})
