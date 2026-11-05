// @vitest-environment happy-dom
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { beforeEach, expect, it, vi } from 'vitest'
import { invalidateChatIdentity } from '../../utils/chatIdentity'
import { DesktopConnections } from './DesktopConnections'

const mocks = vi.hoisted(() => ({ request: vi.fn() }))
vi.mock('../../services/desktopConnections', () => ({ desktopRequest: mocks.request }))
vi.mock('../../services/api', () => ({ default: { get: () => new Promise(() => {}) }, getApiBaseUrl: () => '', getAuthToken: () => '', getLocalApiBaseUrl: () => 'http://127.0.0.1:18743', getDesktopApiBaseUrl: () => 'http://127.0.0.1:18743' }))
vi.mock('../../utils/productWorkspaceNavigation', () => ({ openProductWorkspace: vi.fn() }))
vi.mock('../../utils/appNavigation', () => ({ openAppProduct: vi.fn() }))
beforeEach(() => { invalidateChatIdentity() })
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
    if (path === '/local/inventory') return { status: 'connected', projects: [] }
    if (path === `/${first}/inventory`) return { status: 'unavailable', projects: [] }
    return { status: 'connected', projects: [{ id: 'same-project', name: 'Release crew', kind: 'crew' }] }
  })
  const host = document.createElement('div'), root = createRoot(host)
  try {
    await act(async () => root.render(<DesktopConnections><span>Local draft stays here</span></DesktopConnections>))
    expect(host.textContent).toContain('Local draft stays here')
    expect(host.textContent).toContain('First team unavailable')
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
      { id: 'owned', name: 'Owned crew', kind: 'crew', mode: 'builder', owned_by_caller: true },
      { id: 'shared', name: 'Shared crew', kind: 'crew', mode: 'run', owned_by_caller: false, owner_name: 'Project owner' },
    ] : [] })
  const host = document.createElement('div'), root = createRoot(host)
  try {
    await act(async () => root.render(<DesktopConnections projects={[{ id: 'owned', name: 'Owned crew', kind: 'crew' }, { id: 'weekly', name: 'Weekly goal', kind: 'goal' }]} />))
    const group = host.querySelector('[aria-label="Connected work"]')!
    // One list (owner, 5 Nov: "we only have crews"): another person's Crew sits with yours and names its owner.
    expect(group.textContent).toContain('Owned crew')
    expect(group.textContent).toContain('Shared crew')
    expect(group.textContent).toContain('Project owner')
    expect(group.textContent).not.toContain('Shared with me')
    expect(group.textContent).toContain('Weekly goal')
    expect(group.textContent?.match(/Owned crew/g)).toHaveLength(1)
  } finally {
    await act(async () => root.unmount()); window.electronAPI = previous
  }
})

it('website lists Mac Crews under their Mac, greyed with their last state when the Mac is off (PLAT-856-509)', async () => {
  const previous = window.electronAPI
  window.electronAPI = undefined
  const mac = { device_id: 'dev-1', name: 'MacBook Air', online: false, last_seen: '2026-11-02T10:00:00Z' }
  const host = document.createElement('div'), root = createRoot(host)
  try {
    await act(async () => root.render(<DesktopConnections projects={[
      { id: 'p1', name: 'Login fixes', kind: 'crew', owned_by_caller: true, location: 'mac', mac, state: { state: 'idle', at: '2026-11-02T09:00:00Z' } },
      { id: 'p2', name: 'QA agent', kind: 'crew', owned_by_caller: true, location: 'server' },
    ]} />))
    const macGroup = host.querySelector('section[aria-label="On your Mac: MacBook Air"]')!
    expect(macGroup.textContent).toContain('Login fixes')
    expect(macGroup.textContent).toContain('off · last seen')
    expect(macGroup.textContent).toContain('Idle · last reply')
    expect(macGroup.querySelector('.opacity-60')).not.toBeNull()
    expect(macGroup.textContent).not.toContain('QA agent')
    expect(host.textContent).toContain('On the server')
  } finally {
    await act(async () => root.unmount())
    window.electronAPI = previous
  }
})
