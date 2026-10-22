// @vitest-environment happy-dom
import { act, useState } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, expect, it, vi } from 'vitest'
import { WorkspaceContextButton } from './WorkspaceContextButton'
import { WorkspacePanelAssistant, WorkspacePanelAssistantButton, WorkspacePanelBack, WorkspacePanelNavigationProvider } from './WorkspacePanelNavigation'
import { WorkspacePanelHelpButton } from './WorkspacePanelHelpButton'
import { WorkspacePanelHeader } from './WorkspacePanelHeader'
import { useWorkspacePanelNavigationStore } from '../../stores/useWorkspacePanelNavigationStore'
import { WorkspaceViewHeader } from '../workflow/WorkspaceViewHeader'
import { WorkspaceViewActions } from '../workflow/WorkspaceViewActions'
import { AskAIButton } from '../workflow/AskAIButton'
import { TooltipProvider } from '../ui/tooltip'
import { WorkspacePanelGuideContext } from '../workflow/WorkspacePanelGuideContext'
import { openWorkspacePaneChatWithDraft, sendWorkspacePaneMessageToChat } from '../../utils/workspacePaneChat'
import { useAuthStore } from '../../stores/useAuthStore'
import { useWorkspaceConnectionStore } from '../../stores/useWorkspaceConnectionStore'

vi.mock('../../utils/workspacePaneChat', () => ({ openWorkspacePaneChatWithDraft: vi.fn(), sendWorkspacePaneMessageToChat: vi.fn() }))
vi.mock('../../stores/useAuthStore', async () => {
  const { create } = await import('zustand')
  return { useAuthStore: create(() => ({ user: { id: 'owner' } })) }
})
vi.mock('../../stores/useWorkspaceConnectionStore', async importOriginal => {
  const { create } = await import('zustand')
  return { ...await importOriginal<typeof import('../../stores/useWorkspaceConnectionStore')>(), useWorkspaceConnectionStore: create(() => ({ activeWorkspaceId: 'local', profiles: [{ id: 'local', apiBaseUrl: '' }] })) }
})
Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
let root: Root | undefined
let host: HTMLDivElement
afterEach(async () => {
  if (root) await act(async () => root!.unmount())
  root = undefined; host?.remove()
  useWorkspacePanelNavigationStore.setState({ actions: [], assistants: [], guides: [] })
  useAuthStore.setState({ user: { id: 'owner' } as never })
  useWorkspaceConnectionStore.setState({ activeWorkspaceId: 'local' })
  vi.clearAllMocks()
})
async function render(node: React.ReactNode) {
  if (!root) { host = document.createElement('div'); document.body.append(host); root = createRoot(host) }
  await act(async () => root!.render(<TooltipProvider>{node}</TooltipProvider>))
}

it('provides a header draft action for a view without its own page header', async () => {
  await render(<>
    <WorkspacePanelHelpButton workspacePath="Crew/example" view="shell" />
    <WorkspacePanelNavigationProvider workspacePath="Crew/example" view="shell" chatTarget={{ tabId: 'named-chat' }}>
      <WorkspacePanelAssistant workspacePath="Crew/example" message="Explain the terminal before running anything." />
    </WorkspacePanelNavigationProvider>
  </>)
  const button = host.querySelector<HTMLButtonElement>('button')!
  expect(button.textContent).toContain('Help')
  expect(host.querySelector('[aria-label="Ask AI about this page"]')).toBeNull()
  await act(async () => button.click())
  expect(host.querySelector('[aria-label="Page guide"]')).toBeNull()
  await act(async () => host.querySelector<HTMLButtonElement>('[aria-label="Ask AI about this page"]')!.click())
  expect(openWorkspacePaneChatWithDraft).toHaveBeenCalledWith({ tabId: 'named-chat', message: 'Explain the terminal before running anything.' })
  expect(sendWorkspacePaneMessageToChat).not.toHaveBeenCalled()
})

it('uses one header arrow for nested details, then returns to context', async () => {
  const context = vi.fn()
  function Pane() {
    const [reader, setReader] = useState(true)
    return <><WorkspaceContextButton workspacePath="Crew/example" view="skills" inContext={false} onClick={context} />
      <WorkspacePanelNavigationProvider workspacePath="Crew/example" view="skills">
        {reader ? <><WorkspacePanelBack label="Back to Skills" onBack={() => setReader(false)} /><p>Skill instructions</p></> : <p>Installed skills</p>}
      </WorkspacePanelNavigationProvider></>
  }
  await render(<Pane />)
  expect(host.querySelectorAll('button')).toHaveLength(1)
  expect(host.querySelector('button')?.getAttribute('aria-label')).toBe('Back to Skills')
  await act(async () => host.querySelector<HTMLButtonElement>('button')!.click())
  expect(host.textContent).toBe('Installed skills')
  expect(context).not.toHaveBeenCalled()
  expect(host.querySelector('button')?.getAttribute('aria-label')).toBe('Back to context')
  await act(async () => host.querySelector<HTMLButtonElement>('button')!.click())
  expect(context).toHaveBeenCalledOnce()
})

it('isolates workspace, view, account and server scopes and cleans up on unmount', async () => {
  const back = vi.fn()
  const node = <><WorkspaceContextButton workspacePath="Crew/example" view="skills" inContext={false} onClick={vi.fn()} />
    <WorkspaceContextButton workspacePath="Crew/other" view="skills" inContext={false} onClick={vi.fn()} />
    <WorkspaceContextButton workspacePath="Crew/example" view="files" inContext={false} onClick={vi.fn()} />
    <WorkspacePanelNavigationProvider workspacePath="Crew/example" view="skills"><WorkspacePanelBack label="Back to Skills" onBack={back} /></WorkspacePanelNavigationProvider></>
  await render(node)
  expect([...host.querySelectorAll('button')].map(button => button.getAttribute('aria-label'))).toEqual(['Back to Skills', 'Back to context', 'Back to context'])
  const oldScope = useWorkspacePanelNavigationStore.getState().actions[0].scope
  await act(async () => useAuthStore.setState({ user: { id: 'other' } as never }))
  expect(useWorkspacePanelNavigationStore.getState().actions[0].scope).not.toBe(oldScope)
  const accountScope = useWorkspacePanelNavigationStore.getState().actions[0].scope
  await act(async () => useWorkspaceConnectionStore.setState({ activeWorkspaceId: 'other-server' }))
  expect(useWorkspacePanelNavigationStore.getState().actions[0].scope).not.toBe(accountScope)
  await render(null)
  expect(useWorkspacePanelNavigationStore.getState().actions).toEqual([])
})

it('permits nested read navigation without granting a read-only viewer the edit context', async () => {
  const back = vi.fn(), context = vi.fn()
  const pane = (reader: boolean) => <><WorkspaceContextButton workspacePath="Workflow/example" view="mcp" inContext={false} contextAllowed={false} onClick={context} />
    <WorkspacePanelNavigationProvider workspacePath="Workflow/example" view="mcp">
      {reader && <WorkspacePanelBack label="Back to Skills" onBack={back} />}
    </WorkspacePanelNavigationProvider></>
  await render(pane(true))
  await act(async () => host.querySelector<HTMLButtonElement>('button')!.click())
  expect(back).toHaveBeenCalledOnce(); expect(context).not.toHaveBeenCalled()
  await render(pane(false))
  expect(host.querySelector('button')).toBeNull()
})

it('opens the guide directly with chat assistance inside and preserves the current named chat', async () => {
  const send = vi.fn(), reveal = vi.fn()
  vi.mocked(openWorkspacePaneChatWithDraft).mockResolvedValue({ tabId: 'named-chat' })
  await render(<><nav aria-label="Panel toolbar"><WorkspacePanelHelpButton workspacePath="Crew/example" view="skills" /><WorkspaceContextButton workspacePath="Crew/example" view="skills" inContext={false} onClick={vi.fn()} /></nav><WorkspacePanelNavigationProvider workspacePath="Crew/example" view="skills" chatTarget={{ tabId: 'named-chat' }} onOpenChat={reveal}>
    <WorkspaceViewHeader title="Skills" actions={<WorkspaceViewActions workspacePath="Crew/example" message="Explain these installed skills" onAsk={send} onRefresh={vi.fn()}><button>Specific page action</button></WorkspaceViewActions>} />
    <AskAIButton workspacePath="Crew/example" message="Explain these installed skills" onAsk={send} />
  </WorkspacePanelNavigationProvider></>)
  const help = host.querySelector<HTMLButtonElement>('[aria-label="Help with this page"]')!
  expect([...host.querySelectorAll('nav button')].map(button => button.getAttribute('aria-label'))).toEqual(['Help with this page', 'Back to context'])
  expect(host.querySelector('[aria-label="Ask AI about this page"]')).toBeNull()
  expect(host.querySelector('[aria-label="Walkthrough: Skills"]')).toBeNull()
  expect(host.querySelector('footer')).toBeNull()
  expect(host.textContent).toContain('Specific page action')
  await act(async () => help.click())
  expect(host.querySelector('section[aria-label="Page guide"]')?.textContent).toContain('About Skills')
  const ask = host.querySelector<HTMLButtonElement>('[aria-label="Ask AI about this page"]')!
  expect(ask.closest('[role="dialog"]')?.getAttribute('aria-label')).toBe('Page help')
  expect(ask.title).toContain('Opens a draft in chat')
  expect(host.querySelectorAll('[aria-label="Ask AI about this page"]')).toHaveLength(1)
  await act(async () => ask.click())
  expect(openWorkspacePaneChatWithDraft).toHaveBeenCalledExactlyOnceWith({ tabId: 'named-chat', message: 'Explain these installed skills' })
  expect(reveal).toHaveBeenCalledOnce()
  expect(host.querySelector('[role="dialog"]')).toBeNull()
  expect(send).not.toHaveBeenCalled(); expect(sendWorkspacePaneMessageToChat).not.toHaveBeenCalled()
})

it.each(['Crew/example', 'Workflow/example', 'Workflow/relay'])('keeps Help and Back inside the right panel and opens its guide directly for %s', async workspacePath => {
  const surface = workspacePath.startsWith('Crew/') ? 'crew' : workspacePath.endsWith('/relay') ? 'relay' : 'agentworks'
  const back = vi.fn()
  const pane = (topic: string) => <>
    <nav aria-label="App toolbar" />
    <section aria-label="Right panel"><WorkspacePanelNavigationProvider workspacePath={workspacePath} view="mcp">
      <WorkspacePanelHeader workspacePath={workspacePath} view="mcp" title="Connections" surface={surface} onBack={back} />
      <WorkspacePanelGuideContext.Provider value={surface}><div data-testid="panel-content"><WorkspaceViewHeader title={topic} hideHeader={topic === 'Skills'} helpTopic={topic === 'Skills' ? 'Integrations · Skills' : undefined} /><p>Page content</p></div></WorkspacePanelGuideContext.Provider>
    </WorkspacePanelNavigationProvider></section>
  </>
  await render(pane('Memory'))
  const help = host.querySelector<HTMLButtonElement>('[aria-label="Help with this page"]')!
  expect(host.querySelector('nav button')).toBeNull()
  expect(help.closest('header')?.getAttribute('aria-label')).toBe('Panel navigation')
  expect(help.closest('section')?.getAttribute('aria-label')).toBe('Right panel')
  expect(host.querySelectorAll('[aria-label="Back to context"]')).toHaveLength(1)
  expect(host.querySelector('[data-testid="panel-content"] button')).toBeNull()
  expect(host.querySelector('[data-testid="panel-content"]')?.firstElementChild?.tagName).toBe('H2')
  await act(async () => help.click())
  expect(host.querySelector('[role="dialog"]')?.closest('header')).not.toBeNull()
  expect(host.querySelector('section[aria-label="Page guide"]')).not.toBeNull()
  expect(host.querySelector('[role="dialog"]')?.textContent).toContain(`${surface === 'crew' ? 'Crew' : surface === 'relay' ? 'Flows' : 'Goals'} · Page guide`)
  await act(async () => document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true })))
  expect(host.querySelector('[role="dialog"]')).toBeNull()
  expect(document.activeElement).toBe(help)
  await act(async () => help.click())
  await render(pane('Files'))
  expect(host.querySelector('[role="dialog"]')).toBeNull()
  expect(host.querySelectorAll('[aria-label="Help with this page"]')).toHaveLength(1)
  await act(async () => host.querySelector<HTMLButtonElement>('[aria-label="Back to context"]')!.click())
  expect(back).toHaveBeenCalledOnce()
  await render(pane('Skills'))
  expect(host.querySelector('header h2')?.textContent).toBe('Integrations · Skills')
  expect(host.querySelector('[data-testid="panel-content"]')?.textContent).toBe('Page content')
  await act(async () => host.querySelector<HTMLButtonElement>('[aria-label="Help with this page"]')!.click())
  expect(host.querySelector('section[aria-label="Page guide"]')?.textContent).toContain('custom skills')
  await render(null)
  expect(useWorkspacePanelNavigationStore.getState().guides).toEqual([])
})

it('does not show another workspace’s help', async () => {
  const pane = (path: string) => <>
    <nav><WorkspacePanelHelpButton workspacePath={path} view="mcp" /></nav>
    <WorkspacePanelNavigationProvider workspacePath="Crew/example" view="mcp"><WorkspaceViewHeader title="Memory" /></WorkspacePanelNavigationProvider>
  </>
  await render(pane('Crew/other'))
  expect(host.querySelector('[aria-label="Help with this page"]')).toBeNull()
  await render(pane('Crew/example'))
  expect(host.querySelectorAll('[aria-label="Help with this page"]')).toHaveLength(1)
})

it('does not show stale draft errors or open a new page’s chat when an old operation finishes', async () => {
  let reject!: (reason: Error) => void
  vi.mocked(openWorkspacePaneChatWithDraft).mockImplementation(() => new Promise((_resolve, fail) => { reject = fail }))
  const reveal = vi.fn()
  const pane = (path: string, view: string) => <WorkspacePanelNavigationProvider workspacePath={path} view={view} chatTarget={{ workspacePath: path }} onOpenChat={reveal}>
    <WorkspaceViewHeader title="Settings" actions={<WorkspaceViewActions workspacePath={path} message={`Explain ${path}`} onRefresh={vi.fn()} />} />
    <WorkspacePanelAssistantButton workspacePath={path} view={view} />
  </WorkspacePanelNavigationProvider>
  await render(pane('Workflow/one', 'settings'))
  await act(async () => host.querySelector<HTMLButtonElement>('[aria-label="Ask AI about this page"]')!.click())
  await render(pane('Workflow/two', 'mcp'))
  await act(async () => reject(new Error('Old project is unavailable')))
  expect(host.querySelector('[role="alert"]')).toBeNull()
  expect(host.querySelector<HTMLButtonElement>('[aria-label="Ask AI about this page"]')?.disabled).toBe(false)
  expect(reveal).not.toHaveBeenCalled()
})

it('shows a recoverable draft error and leaves assistance absent on a plain file explorer', async () => {
  vi.mocked(openWorkspacePaneChatWithDraft).mockRejectedValue(new Error('This chat is not available.'))
  await render(<WorkspacePanelNavigationProvider workspacePath="Workflow/one" view="skills">
    <WorkspaceViewHeader title="Skills" actions={<WorkspaceViewActions workspacePath="Workflow/one" message="Explain skills" onRefresh={vi.fn()} />} />
    <WorkspacePanelAssistantButton workspacePath="Workflow/one" view="skills" />
  </WorkspacePanelNavigationProvider>)
  await act(async () => host.querySelector<HTMLButtonElement>('[aria-label="Ask AI about this page"]')!.click())
  expect(host.querySelector('[role="alert"]')?.textContent).toBe('This chat is not available.')
  await render(<WorkspacePanelNavigationProvider workspacePath="Workflow/one" view="files"><WorkspacePanelAssistantButton workspacePath="Workflow/one" view="files" /></WorkspacePanelNavigationProvider>)
  expect(host.querySelector('[aria-label="Ask AI about this page"]')).toBeNull()
})
