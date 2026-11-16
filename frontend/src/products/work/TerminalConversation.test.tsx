// @vitest-environment happy-dom
import React, { act } from 'react'
import { createRoot } from 'react-dom/client'
import { afterEach, expect, it, vi } from 'vitest'
import { TerminalConversation, TerminalConversationHeader, TerminalTranscript } from './TerminalConversation'
import { OwnTerminalAgents } from './OwnTerminalAgents'
import { closeTerminalConversation, useTerminalConversationSelection } from './terminalConversationSelection'
import { teamRequest } from './teamApi'
const f = vi.hoisted(() => ({ identity: 1, draft: vi.fn(), navigate: vi.fn(), team: {} as any }))
vi.mock('./teamApi', () => ({ crewTerminalTarget: async () => ({ project: 'example', connection: 'saved-server' }), teamRequest: vi.fn(), teamFailure: (error: Error) => ({ message: error.message }), useTeamTab: () => f.team }))
vi.mock('../../utils/chatIdentity', () => ({ captureChatIdentity: () => f.identity, isChatIdentityCurrent: (identity: number) => identity === f.identity }))
vi.mock('../../utils/appNavigation', () => ({ openAppProduct: (...args: unknown[]) => f.navigate(...args) }))
vi.mock('../../utils/agentPurposeDraft', () => ({ fillAskFromMyAgentDraft: (...args: unknown[]) => { f.draft(...args); return true } }))
vi.mock('../../stores/useAuthStore', () => ({ useAuthStore: (select: any) => select({ user: { id: 'owner' } }) }))
vi.mock('../../stores/useChatStore', () => ({ useChatStore: Object.assign((select: any) => select({ chatTabs: {} }), { getState: () => ({ addToast: vi.fn() }) }) }))
vi.mock('../../services/api', () => ({ default: { post: vi.fn() }, getAuthToken: () => '', getDesktopApiBaseUrl: () => '' }))
vi.mock('../../hooks/useLiveRefetch', () => ({ useLiveRefetch: () => {} }))
Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
const host = document.createElement('div'); document.body.append(host)
const root = createRoot(host)
const selection = { projectId: 'example', target: { project: 'example', connection: 'saved-server' }, agent: { id: 'terminal', name: 'Helper' }, owner: true, identity: 1, workspace: 'local' }
afterEach(async () => { await act(async () => root.render(null)); closeTerminalConversation(); vi.useRealTimers(); vi.clearAllMocks(); f.identity = 1; f.team = {} })

// PLAT-746-1030: private text must disappear on current refusal; closing stops reads.
it('drops revoked terminal text and cancels future reads when leaving the main view', async () => {
  vi.useFakeTimers()
  Object.defineProperty(document, 'visibilityState', { configurable: true, value: 'visible' })
  vi.mocked(teamRequest).mockResolvedValueOnce({ messages: [{ Role: 'assistant', Parts: [{ Text: 'Native private sentinel' }] }] }).mockRejectedValue(new Error('This conversation is private.'))
  await act(async () => root.render(<TerminalTranscript target={selection.target} agent={selection.agent} />))
  expect(host.textContent).toContain('Native private sentinel')
  await act(async () => vi.advanceTimersByTimeAsync(5000))
  expect(host.textContent).not.toContain('Native private sentinel')
  expect(host.querySelector('[role="alert"]')?.textContent).toBe('This conversation is private.')
  await act(async () => root.render(null))
  const calls = vi.mocked(teamRequest).mock.calls.length
  await act(async () => vi.advanceTimersByTimeAsync(10000))
  expect(vi.mocked(teamRequest).mock.calls.length).toBe(calls)
})

it('opens an owned native agent in the main conversation, with server state and no composer or modal', async () => {
  vi.mocked(teamRequest).mockReset().mockImplementation(async (_target, path) => path === '/terminal-agents' ? { agents: [{ id: 'terminal', name: 'Helper', provider: 'codex-cli', provider_name: 'Codex', online: true, presence: 'online' }] } as never : { messages: [{ Role: 'assistant', Parts: [{ Text: 'Native reply' }] }] } as never)
  f.team = { view: { agents: [{ id: 'terminal', activity: 'running' }], reachable: [{ agent_id: 'terminal', online: true }] } }
  await act(async () => root.render(<OwnTerminalAgents projectId="example" />))
  await act(async () => host.querySelector('button')!.click())
  const chosen = useTerminalConversationSelection.getState().selection!
  expect(chosen).toMatchObject({ projectId: 'example', target: selection.target, agent: selection.agent, owner: true })
  expect(f.navigate).toHaveBeenCalledWith({ surface: 'work', id: 'example' })
  await act(async () => root.render(<><TerminalConversationHeader selection={chosen} close={closeTerminalConversation} /><TerminalConversation selection={chosen} /></>))
  expect(host.textContent).toContain('Native reply')
  expect(host.textContent).toContain('Terminal · this Mac')
  expect(host.textContent).toContain('Working')
  expect(host.textContent).toContain('This chat runs in your terminal. Reply there.')
  expect(host.querySelector('[role="dialog"], textarea, form')).toBeNull()
  expect(vi.mocked(teamRequest).mock.calls.every(call => !call[1]?.endsWith('/ask'))).toBe(true)
  await act(async () => host.querySelector<HTMLButtonElement>('[aria-label="Back to app chat"]')!.click())
  expect(useTerminalConversationSelection.getState().selection).toBeUndefined()
})

it('keeps hidden teammate journals private and uses the existing own-agent draft action', async () => {
  vi.mocked(teamRequest).mockReset()
  f.team = { view: { team: { on: true }, agents: [{ id: 'terminal', name: 'Helper', owner_name: 'Colleague', chat_share: 'agent' }], reachable: [{ agent_id: 'terminal', address: 'agent:colleague/helper' }] } }
  await act(async () => root.render(<TerminalConversation selection={{ ...selection, owner: false }} />))
  expect(host.textContent).toContain("This agent's chat isn't shared")
  expect(vi.mocked(teamRequest)).not.toHaveBeenCalled()
  expect(host.querySelector('textarea, form, [role="dialog"]')).toBeNull()
  await act(async () => host.querySelector('button')!.click())
  expect(f.draft).toHaveBeenCalledWith('example', 'Colleague', 'Helper', 'agent:colleague/helper')
})

it('does not accept native text from a request finished after the account changed', async () => {
  let finish!: (value: unknown) => void
  vi.mocked(teamRequest).mockReset().mockImplementation(() => new Promise(resolve => { finish = resolve }) as never)
  await act(async () => root.render(<TerminalTranscript target={selection.target} agent={selection.agent} />))
  f.identity++
  await act(async () => finish({ messages: [{ Role: 'assistant', Parts: [{ Text: 'Old account text' }] }] }))
  expect(host.textContent).not.toContain('Old account text')
})
