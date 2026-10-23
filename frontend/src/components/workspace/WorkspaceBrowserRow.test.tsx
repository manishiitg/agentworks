// @vitest-environment happy-dom
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { afterEach, expect, it, vi } from 'vitest'
import { WorkspaceBrowserRow } from './WorkspaceBrowserRow'
import api from '../../services/api'

vi.mock('../../services/api', () => ({ default: { get: vi.fn() } }))
// Keep discovery and connection selection real; the live feed has its own tests.
vi.mock('../../hooks/useLiveRefetch', () => ({ useLiveRefetch: vi.fn() }))

const get = api.get as ReturnType<typeof vi.fn>
const cleanups: (() => void)[] = []
afterEach(() => {
  cleanups.splice(0).forEach(cleanup => cleanup())
  vi.clearAllMocks()
  vi.unstubAllGlobals()
})

it.each([
  { name: 'live Playwright capture', selected: false, extensionConnected: false, sessions: [{ kind: 'playwright' }], connected: true },
  { name: 'live server browser', selected: false, extensionConnected: false, sessions: [{ kind: 'headless' }], connected: true },
  { name: 'completed replay only', selected: false, extensionConnected: false, sessions: [{ kind: 'playwright', state: 'completed' }], connected: false },
  { name: 'selected extension without tabs', selected: true, extensionConnected: true, sessions: [], connected: true },
  { name: 'disconnected selected extension with another live browser', selected: true, extensionConnected: false, sessions: [{ kind: 'playwright' }], connected: false },
])('shows the selected connection for $name', async ({ selected, extensionConnected, sessions, connected }) => {
  vi.stubGlobal('IS_REACT_ACT_ENVIRONMENT', true)
  get.mockImplementation(async (url: string) => ({ data: url === '/api/browser/extension'
    ? { selected, connected: extensionConnected, tabs: 0 }
    : { sessions } }))
  const host = document.createElement('div')
  const root = createRoot(host)
  cleanups.push(() => act(() => root.unmount()))
  await act(async () => {
    root.render(<WorkspaceBrowserRow workspacePath="Crew/example-123" profileId="work" onClick={() => {}} />)
  })
  expect(host.querySelector('button')?.textContent).toBe(`Browser${connected ? 'Connected' : 'Not connected'}`)
  expect(get).toHaveBeenCalledWith('/api/browser/extension', expect.objectContaining({
    params: { workspace_path: 'Crew/example-123', profile_id: 'work' },
    skipSessionContext: true,
  }))
  expect(get.mock.calls.some(([url]) => url === '/api/browser/live/sessions')).toBe(!selected)
})
