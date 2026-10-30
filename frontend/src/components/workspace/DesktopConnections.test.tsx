// @vitest-environment happy-dom
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { expect, it, vi } from 'vitest'
import { DesktopConnections } from './DesktopConnections'

const mocks = vi.hoisted(() => ({ request: vi.fn() }))
vi.mock('../../services/desktopConnections', () => ({ desktopRequest: mocks.request }))
vi.mock('../../services/api', () => ({ getLocalApiBaseUrl: () => 'http://127.0.0.1:18743', getDesktopApiBaseUrl: () => 'http://127.0.0.1:18743' }))
vi.mock('../../utils/productWorkspaceNavigation', () => ({ openProductWorkspace: vi.fn() }))
vi.mock('../../utils/appNavigation', () => ({ openAppProduct: vi.fn() }))
Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })

it('identical project IDs open on their owning server while an offline server leaves other groups usable', async () => {
  const first = 'a'.repeat(32), second = 'b'.repeat(32)
  const open = vi.fn().mockResolvedValue(undefined)
  const previous = window.electronAPI
  window.electronAPI = { getDesktopConnection: () => 'local', openDesktopConnection: open }
  mocks.request.mockImplementation(async (path = '') => {
    if (!path) return { connections: [
      { id: first, name: 'First team', server: 'https://one.example.com' },
      { id: second, name: 'Second team', server: 'https://two.example.com' },
    ] }
    if (path === `/${first}/inventory`) return { status: 'unavailable', projects: [] }
    return { status: 'connected', projects: [{ id: 'same-project', name: 'Release crew', kind: 'crew' }] }
  })
  const host = document.createElement('div'), root = createRoot(host)
  try {
    await act(async () => root.render(<DesktopConnections><span>Local draft stays here</span></DesktopConnections>))
    expect(host.textContent).toContain('Local draft stays here')
    expect(host.textContent).toContain('Server unavailable')
    const project = [...host.querySelectorAll('button')].find(button => button.textContent?.includes('Release crew'))!
    await act(async () => project.click())
    expect(open).toHaveBeenCalledWith(second, '/crew/projects/same-project')
  } finally {
    await act(async () => root.unmount())
    window.electronAPI = previous
  }
})

it('a wrapper without the app-owned backend explains how to enable connections instead of silently hiding them', async () => {
  const previous = window.electronAPI
  window.electronAPI = { getDesktopConnection: () => null, openDesktopConnection: vi.fn() }
  const host = document.createElement('div'), root = createRoot(host)
  try {
    await act(async () => root.render(<DesktopConnections><span>Local chats</span></DesktopConnections>))
    expect(host.textContent).toContain('Local chats')
    expect(host.textContent).toContain('desktop launcher')
    expect(host.querySelector('form')).toBeNull()
  } finally {
    await act(async () => root.unmount())
    window.electronAPI = previous
  }
})

it('keeps shared projects in the active server beside the owner navigation', async () => {
  // PLAT-746: the active inventory is not redundant; it also supplies Run projects.
  const id = 'a'.repeat(32), previous = window.electronAPI
  window.electronAPI = { getDesktopConnection: () => id, openDesktopConnection: vi.fn() }
  mocks.request.mockImplementation(async (path = '') => !path
    ? { connections: [{ id, name: 'Team', server: 'https://example.com' }] }
    : { status: 'connected', projects: path === `/${id}/inventory` ? [
      { id: 'owned', name: 'Owned crew', kind: 'crew', mode: 'builder' },
      { id: 'shared', name: 'Shared crew', kind: 'crew', mode: 'run' },
    ] : [] })
  const host = document.createElement('div'), root = createRoot(host)
  try {
    await act(async () => root.render(<DesktopConnections><span>Owned crew</span><span>Weekly goal</span></DesktopConnections>))
    const group = host.querySelector('section[aria-label="Team projects"]')!
    expect(group.textContent).toContain('Shared crew')
    expect(group.textContent).toContain('Weekly goal')
    expect(group.textContent?.match(/Owned crew/g)).toHaveLength(1)
  } finally {
    await act(async () => root.unmount()); window.electronAPI = previous
  }
})
