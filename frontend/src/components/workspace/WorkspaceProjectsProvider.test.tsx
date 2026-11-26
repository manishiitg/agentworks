// @vitest-environment happy-dom
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { afterEach, expect, it, vi } from 'vitest'
import { WorkspaceProjectsProvider, useWorkspaceProjects } from './WorkspaceProjectsProvider'
import { useProductSurfaceStore } from '../../stores/useProductSurfaceStore'
import { agentApi } from '../../services/api'

const f = vi.hoisted(() => ({
  user: { id: 'owner', allowed_products: ['work'] },
}))

vi.mock('../../hooks/useLiveRefetch', () => ({ useLiveRefetch: () => {} }))
vi.mock('../../stores/useAuthStore', async importOriginal => ({
  ...await importOriginal<typeof import('../../stores/useAuthStore')>(),
  useAuthStore: Object.assign((select: (state: unknown) => unknown) => select({ user: f.user }), {
    getState: () => ({ user: f.user }),
    subscribe: () => () => {},
  }),
}))
vi.mock('../../stores/useWorkspaceConnectionStore', async importOriginal => ({
  ...await importOriginal<typeof import('../../stores/useWorkspaceConnectionStore')>(),
  useWorkspaceConnectionStore: Object.assign((select: (state: unknown) => unknown) => select({ activeWorkspaceId: 'local' }), {
    getState: () => ({ activeWorkspaceId: 'local' }),
  }),
}))

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })

afterEach(() => {
  useProductSurfaceStore.setState({ selectedWorkProjectId: null })
  vi.restoreAllMocks()
})

const crew = (id: string, name: string) => ({
  id, name, profile_id: 'work' as const, kind: 'crew' as const, mode: 'builder' as const,
  owned_by_caller: true, workspace_id: 'local', workspace_path: '',
})

function Consumer() {
  const { projects } = useWorkspaceProjects()
  return <div data-projects>{projects.map(p => p.id).join(',')}</div>
}

it('does not refetch the project directory when switching between known projects, but refreshes for a new project', async () => {
  const read = vi.spyOn(agentApi, 'listWorkspaceNavigation')
    .mockResolvedValueOnce({ workspaces: [crew('a', 'Alpha'), crew('b', 'Beta')] })
    .mockResolvedValueOnce({ workspaces: [crew('a', 'Alpha'), crew('b', 'Beta'), crew('c', 'Gamma')] })

  const host = document.createElement('div')
  document.body.append(host)
  const root = createRoot(host)
  try {
    await act(async () => {
      root.render(<WorkspaceProjectsProvider><Consumer /></WorkspaceProjectsProvider>)
      await Promise.resolve()
    })
    expect(read).toHaveBeenCalledOnce()
    expect(host.querySelector('[data-projects]')?.textContent).toBe('a,b')

    // Switching between known projects must not refetch the directory.
    await act(async () => {
      useProductSurfaceStore.setState({ selectedWorkProjectId: 'a' })
      await Promise.resolve()
    })
    await act(async () => {
      useProductSurfaceStore.setState({ selectedWorkProjectId: 'b' })
      await Promise.resolve()
    })
    expect(read).toHaveBeenCalledOnce()

    // Selecting a newly created project not yet in the directory triggers a refresh.
    await act(async () => {
      useProductSurfaceStore.setState({ selectedWorkProjectId: 'c' })
      await Promise.resolve()
    })
    expect(read).toHaveBeenCalledTimes(2)
    expect(host.querySelector('[data-projects]')?.textContent).toBe('a,b,c')
  } finally {
    await act(async () => root.unmount())
    host.remove()
  }
})
