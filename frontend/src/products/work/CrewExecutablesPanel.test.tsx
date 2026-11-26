// @vitest-environment happy-dom
import React, { act } from 'react'
import { createRoot } from 'react-dom/client'
import { beforeEach, expect, it, vi } from 'vitest'

const get = vi.hoisted(() => vi.fn())
const draft = vi.hoisted(() => vi.fn(async () => undefined))
vi.mock('../../services/api', async importOriginal => ({ ...(await importOriginal<object>()), default: { get, patch: vi.fn(), delete: vi.fn() } }))
vi.mock('../../stores/useWorkflowManifestStore', () => ({ useWorkflowManifestStore: { getState: () => ({ refreshWorkflows: async () => undefined }) } }))
vi.mock('../../utils/workspacePaneChat', () => ({ openWorkspacePaneChatWithDraft: draft }))
vi.mock('../../hooks/useLiveRefetch', () => ({ useLiveRefetch: () => undefined }))
// The opened workflow's own screen is covered by its own tests: here it only has to appear.
vi.mock('./CrewExecutableEditor', () => ({ CrewExecutableEditor: ({ item }: { item: { id: string } }) => <div data-testid="editor">{item.id}</div> }))

import { CrewExecutablesPanel } from './CrewExecutablesPanel'
Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })

const executable = (id: string, label: string, description?: string) => ({ id, kind: 'script', workspace_path: `Workflow/${id}`, manifest: { label, description } })

async function mount(executables: unknown[]) {
  get.mockResolvedValue({ data: { executables } })
  const host = document.createElement('div')
  document.body.appendChild(host)
  const root = createRoot(host)
  await act(async () => root.render(<CrewExecutablesPanel projectId="crew-1" chatTabId="tab-1" />))
  await act(async () => undefined)
  return { host, cleanup: async () => { await act(async () => root.unmount()); host.remove() } }
}
const open = (host: HTMLElement, name: string) => host.querySelector<HTMLButtonElement>(`button[title="Open ${name}"]`)!
const button = (host: HTMLElement, text: string) => [...host.querySelectorAll('button')].find(b => b.textContent?.includes(text)) as HTMLButtonElement | undefined

beforeEach(() => { draft.mockClear() })

it('lists every workflow with its description and the total', async () => {
  const { host, cleanup } = await mount([executable('a', 'GitHub tracker', 'Collects open issues each morning'), executable('b', 'Slack notifier')])
  expect(host.querySelector('[data-testid="crew-workflow-count"]')?.textContent).toBe('2 workflows')
  expect(host.textContent).toContain('GitHub tracker')
  expect(host.textContent).toContain('Collects open issues each morning')
  expect(host.textContent).toContain('Slack notifier')
  expect(host.textContent).toContain('No description yet.')
  await cleanup()
})

it('shows the overview even for one workflow, and opens its detail with Open', async () => {
  const { host, cleanup } = await mount([executable('a', 'GitHub tracker', 'Collects issues')])
  expect(host.querySelector('[data-testid="crew-workflow-count"]')?.textContent).toBe('1 workflow')
  expect(host.querySelector('[data-testid="editor"]')).toBeNull()
  await act(async () => { open(host, 'GitHub tracker').click() })
  expect(host.querySelector('[data-testid="editor"]')?.textContent).toBe('a')
  // Back returns to the overview.
  await act(async () => { host.querySelector<HTMLButtonElement>('button[aria-label="Back"]')!.click() })
  expect(host.querySelector('[data-testid="crew-workflow-overview"]')).not.toBeNull()
  await cleanup()
})

it('New workflow asks the Crew chat to start one, from the overview and from a workflow', async () => {
  const { host, cleanup } = await mount([executable('a', 'GitHub tracker')])
  await act(async () => { button(host, 'New workflow')!.click() })
  expect(draft).toHaveBeenCalledTimes(1)
  expect(draft).toHaveBeenCalledWith(expect.objectContaining({ tabId: 'tab-1', message: expect.stringContaining('new workflow') }))
  await act(async () => { open(host, 'GitHub tracker').click() })
  await act(async () => { button(host, 'New workflow')!.click() })
  expect(draft).toHaveBeenCalledTimes(2)
  await cleanup()
})

it('expanding a workflow in the list shows its whole description without opening it', async () => {
  const long = 'Collects open issues each morning. '.repeat(8).trim()
  const { host, cleanup } = await mount([executable('a', 'GitHub tracker', long)])
  const row = host.querySelector('[data-testid="workflow-a"]')!
  await act(async () => { (row.querySelector('button') as HTMLButtonElement).click() })
  expect(row.querySelector('p')?.textContent).toBe(long)
  expect(host.querySelector('[data-testid="editor"]')).toBeNull()
  await cleanup()
})

it('Edit on a workflow opens the chat with its name and ID and asks what to change', async () => {
  const { host, cleanup } = await mount([executable('wf-123', 'GitHub tracker')])
  await act(async () => { open(host, 'GitHub tracker').click() })
  await act(async () => { host.querySelector<HTMLButtonElement>('button[aria-label="Edit this workflow"]')!.click() })
  const message = (draft.mock.calls.at(-1) as unknown as [{ message: string }])[0].message
  expect(message).toContain('GitHub tracker')
  expect(message).toContain('wf-123')
  expect(message).toContain('ask me what I want to edit')
  await cleanup()
})
