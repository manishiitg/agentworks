// @vitest-environment happy-dom
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import ProductAPITriggersView from './ProductAPITriggersView'
import { productWebhooksApi } from '../../api/productWebhooks'
import { TooltipProvider } from '../ui/tooltip'
import { sendWorkspacePaneMessageToChat } from '../../utils/workspacePaneChat'

vi.mock('../../api/productWebhooks', () => ({ productWebhooksApi: { list: vi.fn(), save: vi.fn(), delete: vi.fn() }, apiTriggerURL: (path: string) => `https://agent.example${path}` }))
vi.mock('../../utils/workspacePaneChat', () => ({ sendWorkspacePaneMessageToChat: vi.fn().mockResolvedValue({}) }))
vi.mock('../../services/api', () => ({ getApiBaseUrl: () => '', getAuthToken: () => null, agentApi: { getGmailInboundRoute: vi.fn().mockResolvedValue({ configured: true, route: null, deliveries: [] }) } }))
Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
const scope = { profileId: 'work', projectId: 'p1' }
const trigger = { id: 'trigger-1', name: 'Deploy hook', enabled: true, message: 'Deploy the app', auth_mode: 'bearer' as const, path: '/api/hooks/product/trigger-1', run_destination: 'crew_chat' as const }
const cleanups: (() => void)[] = []
beforeEach(() => {
  vi.mocked(productWebhooksApi.list).mockResolvedValue({ triggers: [trigger, { ...trigger, id: 'trigger-2', name: 'Nightly ping', enabled: false }] })
})
afterEach(() => { cleanups.splice(0).forEach(clean => clean()); vi.clearAllMocks() })
type MountProps = { workspacePath?: string; onAsk?: (message: string) => void; hideHeader?: boolean; refreshToken?: number; onCounts?: (counts: { active: number; paused: number }) => void }
async function mount(props: MountProps = {}) {
  const host = document.createElement('div'); document.body.append(host)
  const root = createRoot(host)
  const render = async (next: MountProps) => { await act(async () => root.render(<TooltipProvider><ProductAPITriggersView scope={scope} {...next} /></TooltipProvider>)) }
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

it('sends incoming-email help to the scoped project chat rather than a workflow', async () => {
  const { host } = await mount({ workspacePath: 'Chats/Work/projects/p1' })
  const button = [...host.querySelectorAll('button')].find(node => node.textContent === 'Ask AI')!
  const clock = vi.spyOn(Date, 'now').mockReturnValue(1000)
  try {
    await act(async () => button.click())
    clock.mockReturnValue(1700)
    await act(async () => button.click())
    expect(sendWorkspacePaneMessageToChat).toHaveBeenCalledExactlyOnceWith({ profileId: 'work', conversationKey: 'p1', message: expect.stringContaining('Incoming email') })
  } finally { clock.mockRestore() }
})
