// @vitest-environment happy-dom
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { afterEach, expect, it, vi } from 'vitest'
import { CrewPublishedAgents } from './CrewPublishedAgents'
import api from '../../services/api'
import { openPublishedAgentChat } from './openPublishedAgentChat'
import { invalidateChatIdentity } from '../../utils/chatIdentity'
vi.mock('../../services/api', () => ({ default: { post: vi.fn(), get: vi.fn() } }))
vi.mock('./openPublishedAgentChat', () => ({ openPublishedAgentChat: vi.fn() }))
Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
afterEach(() => vi.clearAllMocks())
async function render(check: (host: HTMLDivElement) => Promise<void>) {
  const host = document.createElement('div'); document.body.append(host); const root = createRoot(host)
  try { await act(async () => root.render(<CrewPublishedAgents profileId="work" projectId="one" />)); await check(host) }
  finally { await act(async () => root.unmount()); host.remove() }
}
it('explains an empty list in Connections without adding a tab-strip control or starting a conversation', async () => {
  vi.mocked(api.post).mockResolvedValue({ data: { agents: [] } })
  await render(async host => {
    expect(host.textContent).toContain('Bring an agent you already use on your computer into this project.')
    expect(host.querySelector('summary')?.textContent).toBe('How to add an agent')
    expect(api.post).toHaveBeenCalledExactlyOnceWith('/api/workspaces', { action: 'agents', workspace_id: 'work:one' })
    expect(openPublishedAgentChat).not.toHaveBeenCalled()
  })
})
it('reopens the existing private published conversation through the same server action', async () => {
  const chat = { session_id: 'saved', name: 'Research', conversation_key: 'one:chat:agent-research', conversation_id: 'conversation' }
  vi.mocked(api.post).mockResolvedValueOnce({ data: { agents: [{ agent_id: 'research', name: 'Research' }] } }).mockResolvedValueOnce({ data: chat })
  await render(async host => {
    await act(async () => [...host.querySelectorAll('button')].find(button => button.textContent === 'Research')!.click())
    expect(api.post).toHaveBeenLastCalledWith('/api/workspaces', { action: 'open_agent', workspace_id: 'work:one', agent_id: 'research' })
    expect(openPublishedAgentChat).toHaveBeenCalledExactlyOnceWith('one', chat)
  })
})
it('does not open a late response after account identity changes', async () => {
  let finish!: (value: unknown) => void
  vi.mocked(api.post).mockResolvedValueOnce({ data: { agents: [{ agent_id: 'research', name: 'Research' }] } }).mockImplementationOnce(() => new Promise(resolve => { finish = resolve }))
  await render(async host => {
    await act(async () => [...host.querySelectorAll('button')].find(button => button.textContent === 'Research')!.click())
    invalidateChatIdentity()
    await act(async () => finish({ data: { session_id: 'foreign' } }))
    expect(openPublishedAgentChat).not.toHaveBeenCalled()
  })
})
