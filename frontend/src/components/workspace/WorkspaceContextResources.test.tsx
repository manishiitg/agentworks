// @vitest-environment happy-dom
import React, { act } from 'react'
import { createRoot } from 'react-dom/client'
import { afterEach, expect, it, vi } from 'vitest'
const mocks = vi.hoisted(() => ({
  get: vi.fn(),
  extension: vi.fn(),
  webhooks: vi.fn(),
  workflows: vi.fn(),
  watchers: vi.fn(),
  functions: vi.fn(),
}))
vi.mock('../../services/api', () => ({ default: { get: mocks.get } }))
vi.mock('../workflow/ChromeExtensionConnection', () => ({ useChromeExtensionConnection: mocks.extension }))
vi.mock('../../hooks/useLiveRefetch', () => ({ useLiveRefetch: vi.fn() }))
vi.mock('../../api/productWebhooks', () => ({ productWebhooksApi: { list: mocks.webhooks } }))
vi.mock('../../api/workflowWebhooks', () => ({ workflowWebhooksApi: { list: mocks.workflows } }))
vi.mock('../../api/conditionTriggers', () => ({ conditionTriggersApi: { list: mocks.watchers } }))
vi.mock('../../api/crewFunctions', () => ({ crewFunctionsApi: { list: mocks.functions } }))
import { WorkspaceContextBrowser } from './WorkspaceContextBrowser'
import { WorkspaceAutomationLinks } from './WorkspaceAutomationLinks'
Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
afterEach(() => vi.clearAllMocks())
async function render(
  element: React.ReactNode,
  check: (host: HTMLDivElement, rerender: (next: React.ReactNode) => Promise<void>) => Promise<void>
) {
  const host = document.createElement('div')
  document.body.append(host)
  const root = createRoot(host)
  try {
    await act(async () => root.render(element))
    await check(host, async (next) => {
      await act(async () => root.render(next))
    })
  } finally {
    await act(async () => root.unmount())
    host.remove()
  }
}
it('shows the selected named browser even when offline, and opens the existing browser view', async () => {
  mocks.get.mockResolvedValue({ data: { mode: 'headless', port: 9222 } })
  mocks.extension.mockReturnValue({
    loading: false,
    error: '',
    status: {
      selected: true,
      connected: false,
      browser_id: 'edge',
      browsers: [{ id: 'edge', name: 'My Edge' }],
    },
  })
  const open = vi.fn()
  await render(
    <WorkspaceContextBrowser workspacePath="Crew/one" profileId="work" onOpen={open} />,
    async (host) => {
      expect(host.textContent).toContain('My Edge · Offline')
      expect(host.textContent).not.toContain('Workspace browser')
      await act(async () => host.querySelector('button')!.click())
      expect(open).toHaveBeenCalledOnce()
      expect(mocks.get).toHaveBeenCalledWith(
        '/api/browser/workspace',
        expect.objectContaining({ params: { workspace_path: 'Crew/one', profile_id: 'work' } })
      )
    }
  )
})
it('uses saved workspace browser mode when no extension is selected, including read-only scope', async () => {
  mocks.get.mockResolvedValue({ data: { mode: 'cdp', port: 9333 } })
  mocks.extension.mockReturnValue({ loading: false, error: '', status: { selected: false } })
  await render(
    <WorkspaceContextBrowser workspacePath="Workflow/one" profileId="workflow" readOnly onOpen={vi.fn()} />,
    async (host) => {
      expect(host.textContent).toContain('Chrome · port 9333')
      expect(mocks.extension).toHaveBeenCalledWith('Workflow/one', 'workflow', true)
    }
  )
})
it('scopes automation catalogs and keeps errors distinct from an empty count', async () => {
  mocks.webhooks.mockResolvedValue({ triggers: [{}, { kind: 'internal' }, { kind: 'gmail' }] })
  mocks.watchers.mockRejectedValue(new Error('offline'))
  mocks.functions.mockResolvedValue({ functions: [{ name: 'ask', implicit: true }, { name: 'review' }] })
  const open = vi.fn()
  await render(
    <WorkspaceAutomationLinks workspacePath="Crew/one" profileId="work" projectId="one" onOpen={open} />,
    async (host, rerender) => {
      expect(mocks.webhooks).toHaveBeenCalledWith({ profileId: 'work', projectId: 'one' })
      expect(mocks.watchers).toHaveBeenCalledWith('one', 'work')
      const buttons = Array.from(host.querySelectorAll('button'))
      expect(buttons.map((button) => button.textContent)).toEqual([
        'API triggers1',
        'Condition triggersUnavailable',
        'Callable functions1',
      ])
      await act(async () => buttons[1].click())
      expect(open).toHaveBeenCalledWith('conditions')
      await rerender(
        <WorkspaceAutomationLinks
          workspacePath="Crew/one"
          profileId="work"
          projectId="one"
          triggersEnabled={false}
          onOpen={open}
        />
      )
      expect(host.textContent).toBe('')
    }
  )
})
it('reads workflow webhooks and functions from the same existing catalog', async () => {
  mocks.workflows.mockResolvedValue({
    triggers: [{}, { kind: 'function', function: { name: 'review' } }, { kind: 'internal' }],
  })
  mocks.watchers.mockResolvedValue({ triggers: [{ id: 'watcher' }] })
  await render(
    <WorkspaceAutomationLinks
      workspacePath="Workflow/one"
      profileId="agentworks"
      projectId="Workflow/one"
      onOpen={vi.fn()}
    />,
    async (host) => {
      expect(mocks.workflows).toHaveBeenCalledExactlyOnceWith('Workflow/one')
      expect(mocks.webhooks).not.toHaveBeenCalled()
      expect(host.textContent).toBe('API triggers1Condition triggers1FunctionsCallable functions1')
    }
  )
})

it('summarizes an empty schedule/trigger catalog without zero-count rows or chat history', async () => {
  mocks.webhooks.mockResolvedValue({ triggers: [] })
  mocks.watchers.mockResolvedValue({ triggers: [] })
  mocks.functions.mockResolvedValue({ functions: [] })
  await render(
    <WorkspaceAutomationLinks
      workspacePath="Code/one"
      profileId="code"
      projectId="one"
      scheduleCount={0}
      onOpen={vi.fn()}
    />,
    async (host, rerender) => {
      expect(host.textContent).toBe('No schedules or triggers yet.')
      expect(host.querySelectorAll('button')).toHaveLength(0)
      await rerender(
        <WorkspaceAutomationLinks
          workspacePath="Code/one"
          profileId="code"
          projectId="one"
          scheduleCount={2}
          onOpen={vi.fn()}
        />
      )
      expect(host.textContent).toBe('')
    }
  )
})
