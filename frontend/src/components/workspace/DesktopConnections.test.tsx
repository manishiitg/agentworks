// @vitest-environment happy-dom
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { expect, it, vi } from 'vitest'
import { DesktopConnections } from './DesktopConnections'

const mocks = vi.hoisted(() => ({ request: vi.fn() }))
vi.mock('../../services/desktopConnections', () => ({ desktopRequest: mocks.request }))
vi.mock('../../services/api', () => ({ getLocalApiBaseUrl: () => 'http://127.0.0.1:18743' }))
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
