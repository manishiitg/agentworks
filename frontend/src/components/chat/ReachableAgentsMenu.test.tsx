// @vitest-environment happy-dom
import React, { act } from 'react'
import { createRoot } from 'react-dom/client'
import { expect, it, vi } from 'vitest'
const { get } = vi.hoisted(() => ({ get: vi.fn() }))
vi.mock('../../services/api', () => ({ default: { get } }))
vi.mock('../../stores/useAuthStore', () => ({ useAuthStore: (select: (state: unknown) => unknown) => select({ user: { id: 'person-one' } }) }))
vi.mock('../../stores/useWorkspaceConnectionStore', () => ({ useWorkspaceConnectionStore: (select: (state: unknown) => unknown) => select({ activeWorkspaceId: 'workspace-one' }) }))
vi.mock('../../utils/chatIdentity', () => ({ captureChatIdentity: () => ({}), isChatIdentityCurrent: () => true }))
import { ReachableAgentsMenu } from './ReachableAgentsMenu'
Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })

it('loads the authorized directory only on opening and shows hosted and AgentBridge agents together', async () => {
  get.mockResolvedValue({ data: { agents: [
    { id: 'agent:reviewer', kind: 'connected_agent', agent_name: 'reviewer', provider: 'claude', online: true },
    { id: 'workspace:work:one', kind: 'crew', name: 'Research' },
    { id: 'workflow:one', kind: 'goal', name: 'Monthly review' },
    { id: 'workflow:two', kind: 'relay', name: 'Team inbox' },
  ] } })
  const host = document.createElement('div'); document.body.append(host)
  const root = createRoot(host)
  const scroll = HTMLElement.prototype.scrollIntoView; HTMLElement.prototype.scrollIntoView = vi.fn()
  try {
    await act(async () => root.render(<ReachableAgentsMenu />))
    expect(get).not.toHaveBeenCalled()
    await act(async () => { const trigger = host.querySelector<HTMLButtonElement>('button')!; trigger.focus(); trigger.dispatchEvent(new KeyboardEvent('keydown', { key: 'ArrowDown', bubbles: true })) })
    expect(get).toHaveBeenCalledTimes(1)
    expect(get.mock.calls[0][0]).toBe('/api/agent-directory')
    const menu = document.querySelector('[aria-label="Accessible agents"]')!
    expect(menu.textContent).toContain('AgentBridge')
    expect(menu.textContent).toContain('Crew')
    expect(menu.textContent).toContain('Goal')
    expect(menu.textContent).toContain('Relay')
    expect(menu.querySelectorAll('[role="listitem"]')).toHaveLength(4)
    expect(get).toHaveBeenCalledTimes(1)
  } finally { await act(async () => root.unmount()); host.remove(); HTMLElement.prototype.scrollIntoView = scroll; vi.clearAllMocks() }
})
