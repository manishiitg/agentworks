// @vitest-environment happy-dom
import { act, type ReactNode } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, expect, it, vi } from 'vitest'

const api = vi.hoisted(() => ({ teamGet: vi.fn(), accessGet: vi.fn(), directory: vi.fn() }))
let workflows: Array<{ workspace_path: string; my_access: 'owner'; manifest: { id: string; kind?: string } }> = []
vi.mock('../../services/api', () => ({
  authApi: { getWorkflowAccess: api.accessGet, listUserDirectory: api.directory },
  workflowTeamApi: { get: api.teamGet },
}))
vi.mock('../../stores/useAuthStore', () => ({
  useAuthStore: (selector: (state: unknown) => unknown) => selector({ isMultiUserMode: true, user: { id: 'owner', username: 'Owner', is_admin: true } }),
}))
vi.mock('../../stores/useWorkflowManifestStore', () => ({
  useWorkflowManifestStore: (selector: (state: unknown) => unknown) => selector({ workflows, refreshWorkflows: vi.fn() }),
}))
vi.mock('../../hooks/useWorkspaceViewTarget', () => ({ useWorkspaceViewTarget: () => {} }))
vi.mock('./WorkspaceViewHeader', () => ({
  WorkspaceViewHeader: ({ title, subtitle }: { title: ReactNode; subtitle: ReactNode }) => <header>{title}{subtitle}</header>,
}))
vi.mock('./WorkspaceViewActions', () => ({ WorkspaceViewActions: () => null }))
vi.mock('../admin/UsersAdminPanel', () => ({ default: () => null }))
vi.mock('../admin/SlackAdminPanel', () => ({ default: () => null }))

import WorkflowAccessView from './WorkflowAccessView'

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
let root: Root | undefined
afterEach(() => { act(() => root?.unmount()); root = undefined; document.body.innerHTML = ''; window.localStorage?.clear(); vi.clearAllMocks() })

it('loads Sharing without team requests for a Relay, including before discovery, and keeps teams for workflows', async () => {
  const owner = { id: 'owner', username: 'Owner' }
  api.directory.mockResolvedValue({ users: [owner] })
  api.accessGet.mockResolvedValue({ owners: [owner], editors: [], readers: [], my_access: 'owner' })
  api.teamGet.mockResolvedValue({ team: null, routes: [], my_user_id: 'owner', can_manage: true })
  const host = document.createElement('div'); document.body.append(host)
  root = createRoot(host)
  // A trailing slash must not lose the manifest's Relay identity.
  workflows = [{ workspace_path: 'Workflow/relay/', my_access: 'owner', manifest: { id: 'relay', kind: 'relay' } }]
  await act(async () => root?.render(<WorkflowAccessView workspacePath="Workflow/relay" />))
  expect(host.textContent).toContain('Share button there')
  expect(host.textContent).toContain('share its dashboard')
  expect(host.textContent).not.toContain('Goal team')
  expect(host.textContent).not.toContain('Create a plan')
  expect(api.teamGet).not.toHaveBeenCalled()

  workflows = []
  await act(async () => root?.render(<WorkflowAccessView workspacePath="Workflow/relay" relayMode />))
  expect(host.textContent).toContain('Share button there')
  expect(api.teamGet).not.toHaveBeenCalled()

  // Workflows without an explicit kind still support goal teams.
  workflows = [{ workspace_path: 'Workflow/example', my_access: 'owner', manifest: { id: 'example' } }]
  await act(async () => root?.render(<WorkflowAccessView workspacePath="Workflow/example" />))
  expect(host.textContent).toContain('Goal team')
  expect(api.teamGet).toHaveBeenCalledWith('Workflow/example')
})
