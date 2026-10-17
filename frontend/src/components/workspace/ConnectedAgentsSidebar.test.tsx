// @vitest-environment happy-dom
import React, { act } from 'react'
import { createRoot } from 'react-dom/client'
import { afterEach, expect, it, vi } from 'vitest'
const mocks = vi.hoisted(() => ({ userId: 'alice', agents: vi.fn(), open: vi.fn() }))
vi.mock('../../stores/useAuthStore', () => ({ useAuthStore: (select: any) => select({ user: { id: mocks.userId } }) }))
vi.mock('../../stores/useWorkspaceConnectionStore', () => ({ useWorkspaceConnectionStore: (select: any) => select({ activeWorkspaceId: 'local' }) }))
vi.mock('../../services/bullpenApi', () => ({ bullpenApi: { agents: mocks.agents } }))
vi.mock('../../utils/productWorkspaceNavigation', () => ({ openProductWorkspace: mocks.open }))
vi.mock('../../utils/chatIdentity', () => ({ captureChatIdentity: () => 1, isChatIdentityCurrent: () => true }))
import ConnectedAgentsSidebar from './ConnectedAgentsSidebar'
Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
afterEach(() => { vi.clearAllMocks(); mocks.userId = 'alice' })
async function render(check: (host: HTMLDivElement, rerender: () => Promise<void>) => Promise<void>) {
  const host = document.createElement('div'); document.body.append(host)
  const root = createRoot(host)
  try {
    await act(async () => root.render(<ConnectedAgentsSidebar />))
    await check(host, async () => { await act(async () => root.render(<ConnectedAgentsSidebar />)) })
  } finally { await act(async () => root.unmount()); host.remove() }
}
it('shows only connected agents owned by this account without treating connection presence as working status', async () => {
  mocks.agents.mockResolvedValue([
    { agent_id: 'own', agent_name: 'my-reviewer', provider: 'codex', online: true },
    { agent_id: 'offline', agent_name: 'old-agent', online: false },
    { agent_id: 'shared', agent_name: 'someone-else', online: true, shared: true },
  ])
  await render(async (host) => {
    expect(host.textContent).toContain('my-reviewer')
    expect(host.textContent).toContain('Codex')
    expect(host.textContent).not.toContain('old-agent')
    expect(host.textContent).not.toContain('someone-else')
    expect(host.querySelector('[aria-label="Connected"]')).not.toBeNull()
    expect(host.querySelector('[aria-label="Running"]')).toBeNull()
    await act(async () => Array.from(host.querySelectorAll('button')).find((button) => button.textContent?.includes('Open AgentBridge'))!.click())
    expect(mocks.open).toHaveBeenCalledWith('bullpen')
  })
})
it('discards connections returned after the signed-in account changes', async () => {
  let finish!: (agents: any[]) => void
  mocks.agents.mockImplementationOnce(() => new Promise((resolve) => { finish = resolve })).mockResolvedValue([])
  await render(async (host, rerender) => {
    mocks.userId = 'bob'
    await rerender()
    await act(async () => finish([{ agent_id: 'private', agent_name: 'alice-private-agent', online: true }]))
    expect(host.textContent).not.toContain('alice-private-agent')
    expect(host.querySelector('section')).toBeNull()
  })
})

it('omits the empty section without hiding a failed connection lookup', async () => {
  mocks.agents.mockResolvedValue([])
  await render(async host => expect(host.querySelector('section')).toBeNull())
  mocks.agents.mockRejectedValue(new Error('offline'))
  await render(async host => {
    expect(host.textContent).toContain('Connections unavailable')
    expect(host.textContent).not.toContain('No agents connected')
  })
})
