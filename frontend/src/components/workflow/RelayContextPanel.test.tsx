// @vitest-environment happy-dom
import React, { act } from 'react'
import { createRoot } from 'react-dom/client'
import { afterEach, expect, it, vi } from 'vitest'
const mocks = vi.hoisted(() => ({ open: vi.fn(), select: vi.fn(), multiUser: false }))
vi.mock('../../stores/useWorkflowStore', () => ({ useWorkflowStore: (select: (state: unknown) => unknown) => select({ openWorkspaceView: mocks.open }) }))
vi.mock('../../stores/useAuthStore', () => ({ useAuthStore: (select: (state: unknown) => unknown) => select({ isMultiUserMode: mocks.multiUser }) }))
vi.mock('./reportDocuments', () => ({
  useReportDocuments: () => ({ documents: [{ path: 'db/reports/index.html', title: 'Overview' }, { path: 'db/reports/review.html', title: 'Weekly review' }], defaultPath: 'db/reports/index.html' }),
  selectReportDocument: mocks.select,
}))
import { RelayContextPanel } from './RelayContextPanel'
import { WorkflowContextTools } from './WorkflowContextPanel'
Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
afterEach(() => { vi.clearAllMocks(); mocks.multiUser = false })

it('opens existing Flow destinations and scoped outputs without adding Goals-only tools', async () => {
  const host = document.createElement('div')
  const root = createRoot(host)
  try {
    await act(async () => root.render(<RelayContextPanel workspacePath="Workflow/relays/one" />))
    for (const [label, destination, section] of [
      ['Flow & runs', 'flow'], ['API triggers', 'workshop', 'triggers'],
      ['Condition triggers', 'workshop', 'conditions'], ['Costs', 'costs'],
      ['Run logs', 'execution-logs'], ['Files', 'files'],
      ['Chat history', 'workshop', 'chats'], ['Details & instructions', 'identity'], ['Apps & tools', 'mcp'],
      ['MCPs', 'mcp', 'connected'], ['Secrets', 'mcp', 'secrets'], ['Google apps', 'mcp', 'gmail'], ['Slack', 'mcp', 'slack'],
    ]) {
      await act(async () => Array.from(host.querySelectorAll('button')).find(button => button.textContent?.trim() === label)!.click())
      expect(mocks.open).toHaveBeenLastCalledWith(...(section ? [destination, section] : [destination]))
    }
    for (const label of ['Dashboard', 'Weekly review']) {
      await act(async () => Array.from(host.querySelectorAll('button')).find(button => button.textContent?.trim() === label)!.click())
      expect(mocks.open).toHaveBeenLastCalledWith('report')
      expect(mocks.select).toHaveBeenLastCalledWith('Workflow/relays/one', label === 'Dashboard' ? 'db/reports/index.html' : 'db/reports/review.html')
    }
    for (const label of ['Pulse', 'Decisions', 'Browser', 'Schedules', 'Functions', 'Terminal']) expect(host.textContent).not.toContain(label)
    expect(host.querySelector('[aria-label="Workspace setup"]')!.textContent).toBe('Project setupDetails & instructionsApps & tools')
    expect(host.querySelector('[aria-label="All views"]')).not.toBeNull()
  } finally { await act(async () => root.unmount()) }
})

it.each([false, true])('keeps the complete Flow menu and existing Access visibility (multi-user=%s)', async multiUser => {
  mocks.multiUser = multiUser
  const host = document.createElement('div')
  const root = createRoot(host)
  try {
    await act(async () => root.render(<WorkflowContextTools relayMode />))
    await act(async () => (host.querySelector('[aria-label="More workspace tools"]') as HTMLButtonElement).click())
    const labels = Array.from(host.querySelectorAll('[role="menuitem"]')).map(button => button.textContent?.trim())
    expect(labels).toEqual(expect.arrayContaining(['Flow', 'Dashboard', 'Triggers', 'Costs', 'Run logs', 'Files', 'Details & instructions', 'Apps & tools']))
    expect(labels.includes('Access')).toBe(multiUser)
    for (const label of ['Browser', 'Pulse', 'Decisions', 'Schedules', 'Knowledge', 'Backup', 'Publish', 'Notify', 'Workflow playbooks']) expect(labels).not.toContain(label)
    await act(async () => Array.from(host.querySelectorAll('[role="menuitem"]')).find(button => button.textContent?.trim() === 'Triggers')!.dispatchEvent(new MouseEvent('click', { bubbles: true })))
    expect(mocks.open).toHaveBeenLastCalledWith('workshop')
  } finally { await act(async () => root.unmount()) }
})
