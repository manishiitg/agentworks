// @vitest-environment happy-dom
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { afterEach, expect, it, vi } from 'vitest'
import { SidebarThreadsView } from './SidebarThreadsView'
import { clearSideChatListCache } from '../../products/work/sideChatListCache'
import { agentApi } from '../../services/api'
import { useChatStore } from '../../stores/useChatStore'
import { useProductSurfaceStore } from '../../stores/useProductSurfaceStore'

const f = vi.hoisted(() => ({
  openProductWorkspace: vi.fn(),
  openAppProduct: vi.fn(),
  openAgentTab: vi.fn(),
}))

vi.mock('../../hooks/useLiveRefetch', () => ({ useLiveRefetch: () => {} }))
vi.mock('../../utils/productWorkspaceNavigation', () => ({ openProductWorkspace: f.openProductWorkspace }))
vi.mock('../../utils/appNavigation', () => ({ openAppProduct: f.openAppProduct }))
vi.mock('../../utils/openAgentTab', () => ({ openAgentTab: f.openAgentTab }))

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })

afterEach(() => {
  clearSideChatListCache()
  window.localStorage.clear()
  useChatStore.setState({ activeSessionsCache: [], activeTabId: null, chatTabs: {} })
  useProductSurfaceStore.setState({ selectedWorkProjectId: null })
  vi.restoreAllMocks()
  vi.clearAllMocks()
})

it('groups threads into Active now and Recent, filters, pins, and opens a thread', async () => {
  const spy = vi.spyOn(agentApi, 'listAgentProfileSideChats').mockImplementation(async (_profile, projectId) => {
    if (projectId === 'p1') {
      return [
        { conversation_key: 'p1', name: 'Main', is_main: true, session_id: 's-main', last_message_at: '2026-11-25T10:00:00Z' },
        { conversation_key: 'p1:chat:ui', name: 'UI review', session_id: 's-ui', provider: 'claude-code', last_message_at: '2026-11-25T12:00:00Z' },
      ]
    }
    return [
      { conversation_key: 'p2:chat:rel', name: 'Release check', session_id: 's-rel', last_message_at: '2026-11-25T11:30:00Z' },
    ]
  })
  useChatStore.setState({
    activeSessionsCache: [
      { session_id: 's-ui', status: 'running', crew_project_id: 'p1' } as never,
      { session_id: 's-rel', status: 'idle', needs_user_input: true, crew_project_id: 'p2' } as never,
    ],
  })

  const host = document.createElement('div')
  document.body.append(host)
  const root = createRoot(host)
  const projects = [{ id: 'p1', name: 'Acme' }, { id: 'p2', name: 'Release' }]
  try {
    await act(async () => {
      root.render(<SidebarThreadsView projects={projects} scope="local:owner" />)
      await Promise.resolve()
      await Promise.resolve()
    })
    expect(spy).toHaveBeenCalledTimes(2)
    expect(host.querySelector('[aria-label="Filter threads"]')).toBeNull()
    const activeItems = [...host.querySelectorAll('section[aria-label="Active threads"] [role="listitem"]')]
    expect(activeItems[0].textContent).toContain('Release check') // Needs you first
    expect(activeItems[0].textContent).toContain('Needs you')
    expect(activeItems[0].querySelector('[data-thread-status-dot]')).toBeTruthy()
    expect(activeItems[1].textContent).toContain('UI review')
    expect(activeItems[1].textContent).not.toContain('Working')
    expect(activeItems[1].querySelector('svg, img, [aria-label]')).toBeTruthy()
    const recentSection = host.querySelector('section[aria-label="Recent threads"]')!
    expect(recentSection.textContent).toContain('Main')
    expect(recentSection.querySelector('[data-thread-status-dot]')).toBeNull()

    // Pin Main.
    const pinMain = host.querySelector<HTMLButtonElement>('button[aria-label="Pin Main"]')!
    await act(async () => pinMain.click())
    expect(host.querySelector('section[aria-label="Pinned threads"]')?.textContent).toContain('Main')

    // Clicking UI review navigates to its project and agent.
    const uiRow = [...host.querySelectorAll<HTMLButtonElement>('[role="listitem"]')].find(b => b.textContent?.includes('UI review'))!
    await act(async () => uiRow.click())
    expect(f.openProductWorkspace).toHaveBeenCalledWith('work')
    expect(f.openAppProduct).toHaveBeenCalledWith({ surface: 'work', id: 'p1', agent: 'ui' })
  } finally {
    await act(async () => root.unmount())
    host.remove()
  }
})

it('shows an error card when loading fails and retries on click', async () => {
  const spy = vi.spyOn(agentApi, 'listAgentProfileSideChats')
    .mockRejectedValueOnce(new Error('network'))
    .mockResolvedValueOnce([{ conversation_key: 'p1', name: 'Main', is_main: true, session_id: 's1' }])

  const host = document.createElement('div')
  document.body.append(host)
  const root = createRoot(host)
  try {
    await act(async () => {
      root.render(<SidebarThreadsView projects={[{ id: 'p1', name: 'Acme' }]} scope="local:owner" />)
      await Promise.resolve()
      await Promise.resolve()
    })
    expect(host.querySelector('[role="alert"]')?.textContent).toContain('Couldn’t load recent threads')
    const retry = [...host.querySelectorAll<HTMLButtonElement>('button')].find(b => b.textContent === 'Try again')!
    await act(async () => {
      retry.click()
      await Promise.resolve()
      await Promise.resolve()
    })
    expect(spy).toHaveBeenCalledTimes(2)
    expect(host.textContent).toContain('Main')
  } finally {
    await act(async () => root.unmount())
    host.remove()
  }
})
