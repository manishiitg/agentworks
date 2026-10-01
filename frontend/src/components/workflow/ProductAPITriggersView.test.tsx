// @vitest-environment happy-dom
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import ProductAPITriggersView from './ProductAPITriggersView'
import { productWebhooksApi } from '../../api/productWebhooks'

vi.mock('../../api/productWebhooks', () => ({ productWebhooksApi: { list: vi.fn(), save: vi.fn(), delete: vi.fn() }, apiTriggerURL: (path: string) => `https://agent.example${path}` }))
vi.mock('../../services/api', () => ({ getApiBaseUrl: () => '', getAuthToken: () => null, agentApi: { getGmailInboundRoute: vi.fn().mockResolvedValue({ configured: true, route: null, deliveries: [] }) } }))
Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
const scope = { profileId: 'work', projectId: 'p1' }
const trigger = { id: 'trigger-1', name: 'Deploy hook', enabled: true, message: 'Deploy the app', auth_mode: 'bearer' as const, path: '/api/hooks/product/trigger-1', run_destination: 'crew_chat' as const }
const cleanups: (() => void)[] = []
beforeEach(() => {
  vi.mocked(productWebhooksApi.list).mockResolvedValue({ triggers: [trigger, { ...trigger, id: 'trigger-2', name: 'Nightly ping', enabled: false }] })
})
afterEach(() => { cleanups.splice(0).forEach(clean => clean()); vi.clearAllMocks() })
type MountProps = { hideHeader?: boolean; refreshToken?: number; onCounts?: (counts: { active: number; paused: number }) => void }
async function mount(props: MountProps = {}) {
  const host = document.createElement('div'); document.body.append(host)
  const root = createRoot(host)
  const render = async (next: MountProps) => { await act(async () => root.render(<ProductAPITriggersView scope={scope} {...next} />)) }
  await render(props)
  cleanups.push(() => { act(() => root.unmount()); host.remove() })
  return { host, render }
}
it('shows the standalone header with trigger content by default', async () => {
  const { host } = await mount()
  expect(host.textContent).toContain('Send one saved message to this project')
  expect(host.textContent).toContain('Deploy hook')
  expect(host.querySelector('button[aria-label="Refresh project triggers"]')).not.toBeNull()
})
it('hides its header when embedded in the hub but keeps trigger content', async () => {
  const { host } = await mount({ hideHeader: true })
  expect(host.textContent).not.toContain('Send one saved message to this project')
  expect(host.querySelector('button[aria-label="Refresh project triggers"]')).toBeNull()
  expect(host.textContent).toContain('Deploy hook')
  expect(host.textContent).toContain('https://agent.example/api/hooks/product/trigger-1')
})
it('reloads and reports counts when the hub bumps its refresh token', async () => {
  const onCounts = vi.fn()
  const { render } = await mount({ refreshToken: 0, onCounts })
  expect(productWebhooksApi.list).toHaveBeenCalledTimes(1)
  expect(onCounts).toHaveBeenLastCalledWith({ active: 1, paused: 1 })
  await render({ refreshToken: 1, onCounts })
  expect(productWebhooksApi.list).toHaveBeenCalledTimes(2)
  expect(onCounts).toHaveBeenLastCalledWith({ active: 1, paused: 1 })
})
it('lists external webhooks only; caller bindings live under Functions', async () => {
  vi.mocked(productWebhooksApi.list).mockResolvedValue({ triggers: [trigger, { ...trigger, id: 'bind-1', name: 'Called by Alpha Bot', path: '', kind: 'internal', run_destination: 'isolated' as const }] as never })
  const { host } = await mount()
  expect(host.textContent).toContain('Deploy hook')
  expect(host.textContent).not.toContain('Called by Alpha Bot')
  expect(host.textContent).toContain('Own conversation')
})
