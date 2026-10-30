// @vitest-environment happy-dom
import { act, useState } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, expect, it, vi } from 'vitest'
import { WorkspaceContextButton } from './WorkspaceContextButton'
import { WorkspacePanelBack, WorkspacePanelNavigationProvider } from './WorkspacePanelNavigation'
import { WorkspacePanelHeader } from './WorkspacePanelHeader'
import { WorkspacePanelActions, WorkspacePanelHeaderBoundary } from './WorkspacePanelHeaderSlots'
import { useWorkspacePanelNavigationStore } from '../../stores/useWorkspacePanelNavigationStore'
import { WorkspaceViewHeader } from '../workflow/WorkspaceViewHeader'
import { WorkspaceViewActions } from '../workflow/WorkspaceViewActions'
import { TooltipProvider } from '../ui/tooltip'
import { useAuthStore } from '../../stores/useAuthStore'
import { useWorkspaceConnectionStore } from '../../stores/useWorkspaceConnectionStore'

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
  useWorkspacePanelNavigationStore.setState({ actions: [] })
  useAuthStore.setState({ user: { id: 'owner' } as never })
  useWorkspaceConnectionStore.setState({ activeWorkspaceId: 'local' })
  vi.clearAllMocks()
})
async function render(node: React.ReactNode) {
  if (!root) { host = document.createElement('div'); document.body.append(host); root = createRoot(host) }
  await act(async () => root!.render(<TooltipProvider>{node}</TooltipProvider>))
}

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
  const pane = (reader: boolean) => <><WorkspacePanelHeader workspacePath="Workflow/example" view="mcp" title="Connected apps" contextAllowed={false} onBack={context} />
    <WorkspacePanelNavigationProvider workspacePath="Workflow/example" view="mcp">
      {reader && <WorkspacePanelBack label="Back to Skills" onBack={back} />}
    </WorkspacePanelNavigationProvider></>
  await render(pane(true))
  await act(async () => host.querySelector<HTMLButtonElement>('button')!.click())
  expect(back).toHaveBeenCalledOnce(); expect(context).not.toHaveBeenCalled()
  await render(pane(false))
  expect(host.querySelector('button')).toBeNull()
})

it('retains the panel Back arrow and page actions without Help', async () => {
  const back = vi.fn()
  await render(<WorkspacePanelNavigationProvider workspacePath="Crew/example" view="skills" chatTarget={{ tabId: 'named-chat' }}>
    <WorkspacePanelHeader workspacePath="Crew/example" view="skills" title="Skills" onBack={back} />
    <WorkspaceViewHeader title="Skills" actions={<WorkspaceViewActions onRefresh={vi.fn()}><button>Page action</button></WorkspaceViewActions>} />
  </WorkspacePanelNavigationProvider>)
  expect(host.querySelector('[aria-label="Help with this page"]')).toBeNull()
  const returnButton = host.querySelector<HTMLButtonElement>('[aria-label="Back to context"]')!
  expect(returnButton).not.toBeNull()
  await act(async () => returnButton.click())
  expect(back).toHaveBeenCalledOnce()
  expect(host.textContent).toContain('Page action')
})

it.each(['Crew/example', 'Workflow/example', 'Workflow/relay'])('keeps live panel actions in one header and removes them on navigation for %s', async workspacePath => {
  const back = vi.fn()
  const Action = () => {
    const [count, setCount] = useState(0)
    return <WorkspacePanelActions><button onClick={() => setCount(value => value + 1)}>Share {count}</button></WorkspacePanelActions>
  }
  const pane = (view: string, details = false) => <WorkspacePanelNavigationProvider workspacePath={workspacePath} view={view}>
    <WorkspacePanelHeader workspacePath={workspacePath} view={view} title={view} onBack={back} />
    <div data-testid="panel-content">
      <WorkspaceViewHeader title={view === 'browser' ? 'Browser' : 'Skills'} subtitle={view === 'browser' ? 'Chrome or Edge extension' : undefined} actions={view === 'browser' && <button>Settings</button>}
        tabs={view === 'browser' ? { value: 'tabs', onChange: vi.fn(), options: [{ value: 'tabs', label: 'Tabs' }], ariaLabel: 'Browser tabs' } : undefined} />
      {view === 'browser' && <Action />}
      {details && <><WorkspaceViewHeader title="Skill instructions" /><WorkspacePanelBack label="Back to Skills" onBack={back} /></>}
      <WorkspacePanelHeaderBoundary active={false}><WorkspaceViewHeader title="Hidden view" actions={<button>Hidden action</button>} /></WorkspacePanelHeaderBoundary>
    </div>
  </WorkspacePanelNavigationProvider>
  await render(pane('browser'))
  const header = host.querySelector('header[data-testid="workspace-panel-header"]')!
  expect(header.textContent).toContain('Chrome or Edge extension')
  expect([...header.querySelectorAll('button')].map(button => button.textContent)).toEqual(['Settings', 'Share 0', ''])
  await act(async () => header.querySelectorAll('button')[1].click())
  expect(header.textContent).toContain('Share 1')
  expect(host.querySelector('[data-testid="panel-content"] [aria-label="Browser tabs"]')).not.toBeNull()
  expect(header.textContent).not.toContain('Hidden action')
  await render(pane('skills', true))
  expect(header.textContent).not.toContain('Settings')
  expect(header.textContent).not.toContain('Share')
  expect(header.querySelector('[aria-label="Back to Skills"]')).not.toBeNull()
  expect(header.querySelector('[data-workspace-panel-title]:last-child h2')?.textContent).toBe('Skill instructions')
  await render(pane('skills'))
  expect(header.querySelector('[aria-label="Back to Skills"]')).toBeNull()
  expect(header.querySelector('[data-workspace-panel-title]:last-child h2')?.textContent).toBe('Skills')
})
