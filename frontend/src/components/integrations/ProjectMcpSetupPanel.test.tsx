// @vitest-environment happy-dom
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { afterEach, expect, it, vi } from 'vitest'
const api = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn() }))
vi.mock('../../services/api', () => ({ default: api }))
vi.mock('./CliMcpSetupPanel', () => ({ CliMcpSetupPanel: ({ endpoint, projectName }: { endpoint: string; projectName: string }) => <div data-endpoint={endpoint}>{projectName}</div> }))
import { ProjectMcpSetupPanel } from './ProjectMcpSetupPanel'
Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
afterEach(() => vi.resetAllMocks())

it.each(['work', 'code', 'workflow', 'relay'])('uses the server-resolved %s project audience without creating credentials', async kind => {
  const resource = `https://example.com/api/external/v1/mcp/project/${kind}/project-1`
  api.get.mockResolvedValue({ data: { id: 'project-1', kind, title: 'Quarterly reporting', view_url: `${resource}/view`, use_url: `${resource}/use` } })
  const host = document.createElement('div'); const root = createRoot(host)
  try {
    await act(async () => root.render(<ProjectMcpSetupPanel workspacePath="current/workspace" />))
    expect(api.get).toHaveBeenCalledWith('/api/oauth/mcp/project', { params: { workspace: 'current/workspace' } })
    expect(host.querySelector('[aria-label="Project connection"]')).not.toBeNull()
    expect(host.textContent).toContain('This project only.')
    expect(host.textContent).toContain('They sign in with their own account')
    expect(host.querySelector('[data-endpoint]')?.getAttribute('data-endpoint')).toBe(`${resource}/use`)
    const access = host.querySelector('select')!; access.value = 'view'
    await act(async () => access.dispatchEvent(new Event('change', { bubbles: true })))
    expect(host.querySelector('[data-endpoint]')?.getAttribute('data-endpoint')).toBe(`${resource}/view`)
    expect(api.post).not.toHaveBeenCalled()
  } finally { await act(async () => root.unmount()) }
})

it('fails closed when the project cannot be resolved', async () => {
  api.get.mockRejectedValue(new Error('Forbidden'))
  const host = document.createElement('div'); const root = createRoot(host)
  try {
    await act(async () => root.render(<ProjectMcpSetupPanel workspacePath="inaccessible" />))
    expect(host.querySelector('[role="alert"]')).not.toBeNull()
    expect(host.querySelector('[data-endpoint]')).toBeNull()
  } finally { await act(async () => root.unmount()) }
})
