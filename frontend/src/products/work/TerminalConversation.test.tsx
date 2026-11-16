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
vi.mock('../../components/ui/MarkdownRenderer', async () => {
  const { default: Markdown } = await import('react-markdown')
  return { MarkdownRenderer: ({ content }: { content: string }) => <Markdown>{content}</Markdown> }
})
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
  vi.mocked(teamRequest).mockReset().mockImplementation(async (_target, path) => path === '/terminal-agents' ? { agents: [{ id: 'terminal', name: 'Helper', provider: 'codex-cli', provider_name: 'Codex', address: 'agent:own/native', online: true, presence: 'online' }] } as never : { messages: [{ Role: 'assistant', Parts: [{ Text: 'Native reply' }] }] } as never)
  f.team = { view: { agents: [{ id: 'terminal', activity: 'running', engine: 'codex-cli', cli: 'Codex desktop' }], reachable: [{ agent_id: 'terminal', online: true }] } }
  await act(async () => root.render(<OwnTerminalAgents projectId="example" />))
  await act(async () => host.querySelector('button')!.click())
  const chosen = useTerminalConversationSelection.getState().selection!
  expect(chosen).toMatchObject({ projectId: 'example', target: selection.target, agent: selection.agent, owner: true })
  expect(f.navigate).toHaveBeenCalledWith({ surface: 'work', id: 'example' })
  await act(async () => root.render(<><TerminalConversationHeader selection={chosen} close={closeTerminalConversation} /><TerminalConversation selection={chosen} /></>))
  expect(host.textContent).toContain('Native reply')
  expect(host.textContent).toContain('Native agent · Codex desktop running in your terminal on this Mac')
  expect(host.textContent).toContain('Terminal · this Mac')
  expect(host.textContent).toContain('Working')
  expect(host.textContent).toContain('Or reply in your terminal.')
  expect(host.querySelector('[aria-label="How to talk to Helper"]')!.compareDocumentPosition(host.querySelector('[data-terminal-transcript]')!) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy()
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
  await act(async () => [...host.querySelectorAll('button')].find(button=>button.textContent==='✦ Open my agent with this')!.click())
  expect(f.draft).toHaveBeenCalledWith('example', 'Colleague', 'Helper', 'agent:colleague/helper', 'check the release status.')
})

it('does not accept native text from a request finished after the account changed', async () => {
  let finish!: (value: unknown) => void
  vi.mocked(teamRequest).mockReset().mockImplementation(() => new Promise(resolve => { finish = resolve }) as never)
  await act(async () => root.render(<TerminalTranscript target={selection.target} agent={selection.agent} />))
  f.identity++
  await act(async () => finish({ messages: [{ Role: 'assistant', Parts: [{ Text: 'Old account text' }] }] }))
  expect(host.textContent).not.toContain('Old account text')
})

// Actual native readers emit human/ai, not user/assistant. Tool execution detail
// must remain a closed disclosure and must not masquerade as a person's message.
it('formats actual native roles as Markdown and groups tool results in closed cards', async () => {
  vi.mocked(teamRequest).mockReset().mockResolvedValue({ messages: [
    { Role: 'human', Parts: [{ Text: 'Please **check** this' }] },
    { Role: 'ai', Parts: [{ Text: '## Checking\n\nA [reference](https://example.com)' }] },
    { Role: 'tool', tool_id: 'call-1', tool_name: 'Bash', tool_phase: 'call', Parts: [{ Text: '{"command":"printf checked"}' }] },
    { Role: 'tool', tool_id: 'call-1', tool_phase: 'result', Parts: [{ Text: 'checked' }] },
  ] })
  await act(async () => root.render(<TerminalTranscript target={selection.target} agent={selection.agent} />))
  expect(host.querySelector('[aria-label="Your message"] strong')?.textContent).toBe('check')
  expect(host.querySelector('[aria-label="Helper message"] h2')?.textContent).toBe('Checking')
  expect(host.querySelectorAll('details')).toHaveLength(1)
  expect(host.querySelector('details')?.open).toBe(false)
  expect(host.querySelector('summary')?.textContent).toContain('Bash')
  expect(host.querySelector('pre')?.textContent).toContain('checked')
})

it('ends Loading on a stalled request, logs only outcome and time, and allows retry', async () => {
  vi.useFakeTimers()
  const log = vi.spyOn(console, 'info').mockImplementation(() => {})
  vi.mocked(teamRequest).mockReset().mockImplementation((_target, _path, _method, _body, _raw, signal) => new Promise((_resolve, reject) => signal!.addEventListener('abort', () => reject(new Error('private network detail')))) as never)
  await act(async () => root.render(<TerminalTranscript target={selection.target} agent={selection.agent} />))
  await act(async () => vi.advanceTimersByTimeAsync(25_000))
  expect(host.querySelector('[role="status"]')).toBeNull()
  expect(host.querySelector('[role="alert"]')?.textContent).toContain('did not answer in time')
  expect(log).toHaveBeenCalledWith('[NATIVE_CONVERSATION]', expect.objectContaining({ project: 'example', agent: 'terminal', outcome: 'failed', reason: 'timeout', ms: expect.any(Number) }))
  expect(JSON.stringify(log.mock.calls)).not.toContain('private network detail')
  vi.mocked(teamRequest).mockResolvedValue({ messages: [{ Role: 'ai', Parts: [{ Text: '**Ready**' }] }] })
  await act(async () => host.querySelector('button')!.click())
  expect(host.querySelector('strong')?.textContent).toBe('Ready')
  log.mockRestore()
})

it('copies the guidance example and opens only an unsent own-agent draft, clearing the native selection', async()=>{
  const writeText=vi.fn().mockResolvedValue(undefined)
  Object.defineProperty(navigator,'clipboard',{configurable:true,value:{writeText}})
  vi.mocked(teamRequest).mockResolvedValue({messages:[]})
  const own={...selection,agent:{...selection.agent,address:'agent:owner/native'}}
  useTerminalConversationSelection.setState({selection:own})
  await act(async()=>root.render(<TerminalConversation selection={own}/>))
  await act(async()=>host.querySelector<HTMLButtonElement>('[aria-label="Copy example prompt"]')!.click())
  expect(writeText).toHaveBeenCalledWith('Ask Helper to check the release status.')
  await act(async()=>[...host.querySelectorAll('button')].find(button=>button.textContent==='✦ Open my agent with this')!.click())
  expect(f.draft).toHaveBeenCalledWith('example','you','Helper','agent:owner/native','check the release status.')
  expect(useTerminalConversationSelection.getState().selection).toBeUndefined()
  expect(host.querySelector('textarea,form')).toBeNull()
})
