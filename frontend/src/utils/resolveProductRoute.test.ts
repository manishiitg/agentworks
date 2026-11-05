// @vitest-environment happy-dom
import { describe, expect, it, vi } from 'vitest'

const mocks = vi.hoisted(() => ({ get: vi.fn() }))
vi.mock('../services/api', () => ({ default: { get: mocks.get }, agentApi: { listWorkflowManifests: vi.fn() }, getApiBaseUrl: () => '', getAuthToken: () => '' }))
vi.mock('../platform/chat/productProjects', () => ({ loadProductProjects: vi.fn(async () => [{ id: 'example' }]) }))
vi.mock('../products/work/projectProduct', () => ({ projectProductConfig: () => ({ projectsRoot: 'Chats', profileId: 'code', listsSharedProjects: false }) }))
vi.mock('../products/work/workSessions', () => ({ loadSharedWorkSessions: vi.fn(async () => []) }))
vi.mock('../products/work/WorkSurface', () => ({
  normalizeWorkWorkspaceView: (value: unknown) => (value === 'files' || value === 'shell' || value === 'costs' ? value : null),
  SHARED_CREW_WORKSPACE_PANELS: new Set(['files', 'memory']),
}))
vi.mock('./productWorkspaceNavigation', () => ({ openProductWorkspace: vi.fn() }))
vi.mock('./workflowNavigation', () => ({ selectWorkflowPreset: vi.fn() }))
vi.mock('../stores/useProductSurfaceStore', () => ({
  useProductSurfaceStore: { getState: () => ({ setSelectedWorkProjectId: vi.fn(), setLastVideoProjectId: vi.fn() }) },
}))

import { resolveProductRoute } from './resolveProductRoute'

describe('resolveProductRoute view fallback', () => {
  it('carries the server refusal when a Crew is no longer in the permitted inventory', async () => {
    mocks.get.mockRejectedValueOnce({ response: { status: 403, data: { error: { code: 'team_forbidden', message: 'Ask the owner to add you to the Team.' } } } })
    await expect(resolveProductRoute({ surface: 'work', id: 'removed' }, new AbortController().signal)).rejects.toThrow('Ask the owner to add you to the Team.')
  })
  it('opens the project with the default view for an unknown view name', async () => {
    const activate = await resolveProductRoute({ surface: 'work', id: 'example', view: 'pulse' }, new AbortController().signal)
    expect(typeof activate).toBe('function')
  })
  it('still resolves a known view', async () => {
    const activate = await resolveProductRoute({ surface: 'work', id: 'example', view: 'shell' }, new AbortController().signal)
    expect(typeof activate).toBe('function')
  })
})
