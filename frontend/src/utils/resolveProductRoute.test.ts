// @vitest-environment happy-dom
import { describe, expect, it, vi } from 'vitest'

vi.mock('../services/api', () => ({ agentApi: { listWorkflowManifests: vi.fn() }, getApiBaseUrl: () => '', getAuthToken: () => '' }))
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
  it('opens the project with the default view for an unknown view name', async () => {
    const activate = await resolveProductRoute({ surface: 'work', id: 'example', view: 'pulse' }, new AbortController().signal)
    expect(typeof activate).toBe('function')
  })
  it('still resolves a known view', async () => {
    const activate = await resolveProductRoute({ surface: 'work', id: 'example', view: 'shell' }, new AbortController().signal)
    expect(typeof activate).toBe('function')
  })
})
