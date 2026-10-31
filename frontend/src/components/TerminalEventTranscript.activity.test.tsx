// @vitest-environment happy-dom
import React, { act } from 'react'
import { createRoot } from 'react-dom/client'
import { afterEach, describe, expect, it, vi } from 'vitest'
import type { PollingEvent } from '../services/api-types'
import { TerminalEventTranscript } from './TerminalEventTranscript'
import { CleanConversationSurface } from './CleanConversationSurface'
import api from '../services/api'
import { openReplyChanges } from './workspace/chatFileNavigation'
import { setProjectMode } from '../products/work/codeLocalFiles'
import { useChatRuntimeActivity } from '../hooks/useChatRuntimeActivity'
import { useChatStore, type ChatTab } from '../stores/useChatStore'
import type { ActiveSessionInfo } from '../services/api-types'

vi.mock('react-virtuoso', () => ({
  Virtuoso: ({ data, firstItemIndex, itemContent, computeItemKey, components, context }: { components?: { Footer?: React.ComponentType<{ context?: unknown }> }; context?: unknown; data: unknown[]; firstItemIndex: number; itemContent: (index: number, item: unknown) => React.ReactNode; computeItemKey: (index: number, item: unknown) => string }) => (
    <div>{data.map((item, index) => <div key={computeItemKey(firstItemIndex + index, item)}>{itemContent(firstItemIndex + index, item)}</div>)}{components?.Footer && <components.Footer context={context} />}</div>
  ),
}))
vi.mock('./workspace/chatFileNavigation', () => ({ openChatFiles: vi.fn(), openReplyChanges: vi.fn() }))
vi.mock('./FileContentViewer', () => ({ FileContentViewerBody: ({ source }: { source: { fileContent: string } }) => <pre data-testid="saved-preview">{source.fileContent}</pre> }))
vi.mock('./events/EventDispatcher', () => ({ EventDispatcher: () => null }))
vi.mock('./ui/MarkdownRenderer', () => ({ ConversationMarkdownRenderer: ({ content }: { content: string }) => <div>{content}</div> }))
type TerminalEventTranscriptProps = React.ComponentProps<typeof TerminalEventTranscript>
const cleanups: Array<() => void> = []
afterEach(() => { cleanups.splice(0).forEach(cleanup => cleanup()); vi.unstubAllGlobals() })
function event(id: string, type: string, data: Record<string, unknown>): PollingEvent {
  return { id, type, timestamp: '2026-10-01T07:00:00Z', data: { type, data } } as PollingEvent
}
const user = event('user', 'user_message', { content: 'Check this project' })
const completed = event('answer', 'llm_generation_end', { content: 'Checked the project', duration: 32_100_000_000 })
const running = { state: 'running', label: 'running' } as const
async function mount(props: Partial<TerminalEventTranscriptProps>) {
  vi.stubGlobal('IS_REACT_ACT_ENVIRONMENT', true)
  const host = document.createElement('div'); document.body.appendChild(host)
  const root = createRoot(host)
  const render = async (next: Partial<TerminalEventTranscriptProps>) => {
    await act(async () => { root.render(<TerminalEventTranscript terminal={null} events={[]} {...next} />) })
  }
  await render(props)
  cleanups.push(() => { act(() => root.unmount()); host.remove() })
  return { host, render }
}
// The live-activity indicator: the shared working line while the agent runs (its star is the motion, no separate spinner),
// the small status dot for other states such as waiting for input.
function indicators(host: HTMLElement) { return host.querySelectorAll('[data-testid="agent-runtime-activity"], [data-testid="working-status"]') }
function footer(host: HTMLElement) { return host.querySelector('[data-testid="transcript-activity-footer"]') as HTMLElement }
function headers(host: HTMLElement) { return host.querySelectorAll('[data-testid="terminal-clear-assistant-header"]') }

describe('activity belongs to the current agent turn', () => {
  it('opens recorded inspector tool input and output directly from one row click', async () => {
    const calls=[
      event('inspect-tool','tool_call_start',{tool_call_id:'inspect-tool',tool_name:'read_file',arguments:'{"path":"status.json"}'}),
      event('inspect-result','tool_call_end',{tool_call_id:'inspect-tool',tool_name:'read_file',result:'{"status":"INITIALIZED"}'}),
    ]
    const {host,render}=await mount({events:[user,calls[0]],toolLayout:'rows'})
    expect(host.querySelector('[aria-label="Tool running"]')).not.toBeNull()
    await render({events:[user,...calls],toolLayout:'rows'})
    expect(host.querySelector('[aria-label="Tool running"]')).toBeNull()
    expect(host.querySelector('[data-testid="terminal-clear-tool-batch-toggle"]')).toBeNull()
    const row=host.querySelector('[data-testid="terminal-clear-tool-call"]')!
    expect(row.textContent).not.toContain('INITIALIZED')
    await act(async()=>row.querySelector<HTMLButtonElement>('button')!.click())
    expect(row.textContent).toContain('Input')
    expect(row.textContent).toContain('Output')
    expect(row.textContent).toContain('status.json')
    expect(row.textContent).toContain('INITIALIZED')
  })

  // Owner 2026-10-27: alternating Thinking and tool rows read as a ladder; the steps between two replies fold into one
  // closed Working row that lists them in order when opened.
  it('folds alternating thinking and tool batches into one closed Working row', async () => {
    const steps = [
      event('think-1', 'conversation_thinking', { thinking: 'Look at the logs first.' }),
      event('call-a', 'tool_call_start', { tool_call_id: 'call-a', tool_name: 'read_file' }),
      event('result-a', 'tool_call_end', { tool_call_id: 'call-a', tool_name: 'read_file', result: 'ok' }),
      event('think-2', 'conversation_thinking', { thinking: 'Now compare the commits.' }),
      event('call-b', 'tool_call_start', { tool_call_id: 'call-b', tool_name: 'execute_shell_command' }),
      event('result-b', 'tool_call_end', { tool_call_id: 'call-b', tool_name: 'execute_shell_command', result: 'ok' }),
    ]
    const { host } = await mount({ events: [user, ...steps] })
    const groups = host.querySelectorAll('[data-testid="terminal-working-group"]')
    expect(groups).toHaveLength(1)
    expect(groups[0].textContent).toContain('2 steps')
    expect(host.querySelector('[data-testid="terminal-clear-thinking-batch-toggle"]')).toBeNull()
    await act(async () => { groups[0].querySelector<HTMLButtonElement>('button')!.click() })
    expect(host.querySelectorAll('[data-testid="terminal-clear-thinking-batch-toggle"]')).toHaveLength(2)
  })

  // PLAT-746-158 A5: a last-position group was animated even in saved history,
  // while a genuinely running command only said “now: 1 tool call”.
  it('uses the server action while running and retains completed activity across the next turn', async () => {
    const thinking = event('thinking', 'conversation_thinking', { thinking: 'Check the project.' })
    const start = event('start', 'tool_call_start', { tool_call_id: 'test', tool_name: 'execute_shell_command' })
    const phase = event('phase', 'turn_phase', { phase: 'running', text: 'Running tests', since: start.timestamp })
    const end = { ...event('end', 'tool_call_end', { tool_call_id: 'test', tool_name: 'execute_shell_command', result: 'passed' }), timestamp: '2026-10-01T07:03:00Z' }
    const finished = { ...completed, timestamp: '2026-10-01T07:03:05Z' }
    const { host, render } = await mount({ events: [user, thinking, start, phase], runtimeActivity: running })
    const group = () => host.querySelector('[data-testid="terminal-working-group"]')!
    expect(group().textContent).toContain('Running tests')
    expect(group().querySelector('span[aria-hidden="true"]')).not.toBeNull()
    await render({ events: [user, thinking, start, phase, end, finished] })
    expect(group().textContent).toContain('3m')
    expect(group().textContent).not.toContain('Running tests')
    expect(group().querySelector('span[aria-hidden="true"]')).toBeNull()
    await render({ events: [user, thinking, start, phase, end, finished, { ...event('next', 'user_message', { content: 'Now inspect the report' }), timestamp: '2026-10-01T07:04:00Z' }], runtimeActivity: running })
    expect(group().textContent).toContain('3m')
    expect(group().querySelector('span[aria-hidden="true"]')).toBeNull()
    await act(async () => group().querySelector<HTMLButtonElement>('button')!.click())
    expect(group().querySelector('[data-testid="terminal-working-group-steps"]')).not.toBeNull()
  })

  it('mounts product activity details only when opened, while retaining every recorded call', async () => {
    vi.stubGlobal('IS_REACT_ACT_ENVIRONMENT', true)
    const calls = Array.from({ length: 10 }, (_, index) => [
      event(`start-${index}`, 'tool_call_start', { tool_call_id: `call-${index}`, tool_name: 'read_file', arguments: '{"path":"report.txt"}' }),
      event(`end-${index}`, 'tool_call_end', { tool_call_id: `call-${index}`, tool_name: 'read_file', result: 'unchanged' }),
    ]).flat()
    const host = document.createElement('div'); document.body.appendChild(host)
    const root = createRoot(host)
    cleanups.push(() => { act(() => root.unmount()); host.remove() })
    await act(async () => root.render(<CleanConversationSurface events={[user, ...calls, completed]} isStreaming={false} isRestoring={false} streamingText="" />))
    const details = host.querySelector<HTMLDetailsElement>('[data-testid="clean-production-activity"]')!
    expect(details.querySelectorAll('[data-status]')).toHaveLength(0)
    await act(async () => { details.open = true; details.dispatchEvent(new Event('toggle')) })
    expect(details.querySelectorAll('[data-status]')).toHaveLength(10)
    await act(async () => { details.open = false; details.dispatchEvent(new Event('toggle')) })
    expect(details.querySelectorAll('[data-status]')).toHaveLength(0)
  })

  it('renders one six-call group across duplicate native progress updates', async () => {
    const calls = Array.from({ length: 6 }, (_, index) => [
      event(`progress-${index}`, 'streaming_chunk', { source: 'transcript', content: 'The download finished; the full run is still in progress.' }),
      event(`call-${index}`, 'tool_call_start', { tool_call_id: `call-${index}`, tool_name: 'execute_shell_command' }),
      event(`result-${index}`, 'tool_call_end', { tool_call_id: `call-${index}`, tool_name: 'execute_shell_command', result: 'ok' }),
    ]).flat()
    const { host } = await mount({ events: [user, ...calls] })
    const groups = host.querySelectorAll('[data-testid="terminal-clear-tool-batch-toggle"]')
    expect(groups).toHaveLength(1)
    expect(groups[0].textContent).toBe('6 tool calls')
    await act(async () => { (groups[0] as HTMLButtonElement).click() })
    expect(host.textContent?.match(/execute_shell_command/g)).toHaveLength(6)
  })

  it('renders one tool chip when invisible turn phases interleave tool calls', async () => {
    const calls = Array.from({ length: 6 }, (_, index) => [
      event(`call-${index}`, 'tool_call_start', { tool_call_id: `call-${index}`, tool_name: 'execute_shell_command' }),
      event(`phase-${index}`, 'turn_phase', { phase: 'running', text: 'Working', since: '2026-10-19T15:46:47Z' }),
      event(`result-${index}`, 'tool_call_end', { tool_call_id: `call-${index}`, tool_name: 'execute_shell_command', result: 'ok' }),
    ]).flat()
    const { host, render } = await mount({ events: [user, ...calls], runtimeActivity: running })
    const groups = host.querySelectorAll('[data-testid="terminal-clear-tool-batch-toggle"]')
    expect(groups).toHaveLength(1)
    expect(groups[0].textContent).toBe('6 tool calls')
    expect(indicators(host)).toHaveLength(1)
    // Restored history uses the same grouping after the live Working line ends.
    await render({ events: [user, ...calls, completed] })
    expect(host.querySelectorAll('[data-testid="terminal-clear-tool-batch-toggle"]')).toHaveLength(1)
    expect(host.textContent).toContain('Checked the project')
  })

  it('keeps the header loading through idle polls and replayed older completions until the new turn ends', async () => {
    vi.stubGlobal('IS_REACT_ACT_ENVIRONMENT', true)
    const original = useChatStore.getState()
    const oldCompletion = { ...completed, timestamp: '2026-10-01T06:59:00Z' }
    useChatStore.setState({
      getActiveSessions: vi.fn().mockResolvedValue([]),
      activeSessionsCache: [{ session_id: 'chat-a', status: 'completed' } as ActiveSessionInfo],
      chatTabs: { a: { tabId: 'a', sessionId: 'chat-a', isStreaming: true } as ChatTab },
      tabEvents: { 'chat-a': [user] },
    })
    function Chat() {
      const activity = useChatRuntimeActivity('a')
      const events = useChatStore(state => state.tabEvents['chat-a'])
      return <TerminalEventTranscript terminal={null} events={events} runtimeActivity={activity} />
    }
    const host = document.createElement('div'); document.body.appendChild(host)
    const root = createRoot(host)
    cleanups.push(() => { act(() => root.unmount()); host.remove(); useChatStore.setState(original, true) })
    await act(async () => { root.render(<Chat />) })
    expect(indicators(host)).toHaveLength(1)
    for (const isStreaming of [false, true, false]) {
      await act(async () => { useChatStore.setState({
        chatTabs: { a: { tabId: 'a', sessionId: 'chat-a', isStreaming, isCompleted: !isStreaming } as ChatTab },
        tabEvents: { 'chat-a': [user, oldCompletion] },
      }) })
      expect(indicators(host)).toHaveLength(1)
    }
    await act(async () => { useChatStore.setState({ tabEvents: { 'chat-a': [user, oldCompletion,
      { ...event('new-done', 'unified_completion', { final_result: 'Finished' }), timestamp: '2026-10-01T07:01:00Z' },
    ] } }) })
    expect(indicators(host)).toHaveLength(0)
  })

  it('renders repeated native replies on successive turns, live and after restoring the chat', async () => {
    const turns = [1, 2, 3].map(turn => [
      event(`user-${turn}`, 'user_message', { content: 'Which model are you using?' }),
      { ...event(`gen-${turn}`, 'llm_generation_end', { content: 'GPT-6 (Codex).' }), execution_id: 'main:chat-a', execution_kind: 'main_agent' },
      { ...event(`done-${turn}`, 'unified_completion', { final_result: 'GPT-6 (Codex).', status: 'completed' }), execution_id: 'main:chat-a', execution_kind: 'main_agent' },
    ])
    const { host, render } = await mount({ events: turns[0] })
    const answerCount = (node: HTMLElement) => node.textContent?.match(/GPT-6 \(Codex\)\./g)?.length || 0
    expect(answerCount(host)).toBe(1)
    await render({ events: turns.slice(0, 2).flat() })
    expect(answerCount(host)).toBe(2)
    await render({ events: turns.flat() })
    expect(answerCount(host)).toBe(3)
    expect(headers(host)).toHaveLength(3)
    const restored = await mount({ events: turns.flat() })
    expect(answerCount(restored.host)).toBe(3)
  })

  it('does not mark foreground polling done when a provisional follow-up arrives', async () => {
    const narrative = event('narrative', 'llm_generation_end', { content: 'Waiting for the review', duration: 32_100_000_000 })
    const tool = event('poll-tool', 'tool_call_start', { tool_call_id: 'poll', tool_name: 'Bash', arguments: '{"command":"check-review"}' })
    const queued = event('queued', 'user_message', { content: 'One more comment', metadata: { provisional: true, delivery_status: 'queued' } })
    const phase = event('phase', 'turn_phase', { phase: 'running', since: user.timestamp, turn_started_at: user.timestamp, last_activity_at: user.timestamp })
    const { host, render } = await mount({ events: [user, narrative, tool, phase, queued], runtimeActivity: running })
    expect(host.querySelector('[data-testid="terminal-clear-turn-done"]')).toBeNull()
    await render({ events: [user, narrative, tool, phase, queued], runtimeActivity: { state: 'ready', label: 'idle' } })
    expect(host.querySelector('[data-testid="terminal-clear-turn-done"]')).toBeNull()
    const restored = event('restored', 'llm_generation_end', { content: 'Still waiting', restored_intermediate_update: true })
    await render({ events: [user, restored], runtimeActivity: { state: 'ready', label: 'idle' } })
    expect(host.textContent).toContain('Still waiting')
    expect(host.querySelector('[data-testid="terminal-clear-turn-done"]')).toBeNull()
    await render({ events: [user, completed], runtimeActivity: { state: 'ready', label: 'idle' } })
    expect(host.querySelector('[data-testid="terminal-clear-turn-done"]')).not.toBeNull()
  })

  it('removes the footer spinner when the turn completes while cache and tab flags remain running', async () => {
    vi.stubGlobal('IS_REACT_ACT_ENVIRONMENT', true)
    const original = useChatStore.getState()
    useChatStore.setState({
      getActiveSessions: vi.fn().mockResolvedValue([]),
      activeSessionsCache: [{ session_id: 'chat-a', status: 'running' } as ActiveSessionInfo],
      chatTabs: { a: { tabId: 'a', sessionId: 'chat-a', isStreaming: true } as ChatTab },
      tabEvents: { 'chat-a': [user] },
    })
    function Chat() {
      const activity = useChatRuntimeActivity('a')
      const events = useChatStore(state => state.tabEvents['chat-a'])
      return <TerminalEventTranscript terminal={null} events={events} runtimeActivity={activity} />
    }
    const host = document.createElement('div'); document.body.appendChild(host)
    const root = createRoot(host)
    cleanups.push(() => { act(() => root.unmount()); host.remove(); useChatStore.setState(original, true) })
    await act(async () => { root.render(<Chat />) })
    expect(indicators(host)).toHaveLength(1)
    await act(async () => { useChatStore.setState({ tabEvents: { 'chat-a': [user, completed] } }) })
    expect(indicators(host)).toHaveLength(0)
    expect(host.querySelector('[data-testid="terminal-clear-turn-done"]')?.textContent).toContain('for 32s')
    await act(async () => {
      useChatStore.setState({ tabEvents: { 'chat-a': [user, completed, event('next-user', 'user_message', { content: 'Check again' })] } })
    })
    expect(indicators(host)).toHaveLength(1)
    expect(footer(host).querySelector('[role="status"]')).not.toBeNull()
    expect(headers(host)[0].querySelector('[role="status"]')).toBeNull()
  })
  it.each([{ events: [] }, { events: [user] }])('shows the agent before any response or tool event arrives', async ({ events }) => {
    const { host } = await mount({ events, runtimeActivity: running })
    expect(headers(host)).toHaveLength(1)
    expect(indicators(host)).toHaveLength(1)
    expect(footer(host).querySelector('[data-testid="working-glyph"]')).not.toBeNull()
    expect(footer(host).querySelector('.animate-spin')).toBeNull()
    expect(footer(host).textContent).toContain('Working…')
    expect(headers(host)[0].querySelector('[role="status"]')).toBeNull()
  })
  it('keeps a single stable header and spinner through streamed token updates', async () => {
    const props = { events: [user], runtimeActivity: running }
    const { host, render } = await mount(props)
    const spinner = indicators(host)[0].firstElementChild
    await render({ ...props, streamingText: 'Checking' })
    await render({ ...props, streamingText: 'Checking the project files' })
    expect(headers(host)).toHaveLength(1)
    expect(indicators(host)[0].firstElementChild).toBe(spinner)
    expect(host.textContent).toContain('Checking the project files')
    expect(host.querySelector('[aria-label="Writing"]')).toBeNull()
  })
  it('keeps tool work and live prose under the same agent header', async () => {
    const { host } = await mount({ events: [user, event('tool', 'tool_call_start', { tool_name: 'read_workspace_file' })], streamingText: 'Reading the files', runtimeActivity: running })
    expect(headers(host)).toHaveLength(1)
    expect(indicators(host)).toHaveLength(1)
    expect(host.querySelector('[data-testid="terminal-clear-tool-batch"]')).not.toBeNull()
  })
  it('never animates earlier replies while a new reply is pending', async () => {
    const { host } = await mount({ events: [user, completed, event('next-user', 'user_message', { content: 'Check again' })], runtimeActivity: running })
    expect(headers(host)).toHaveLength(2)
    expect(host.querySelector('[data-testid="terminal-clear-turn-done"]')?.textContent).toContain('for 32s')
    expect(headers(host)[0].querySelector('[role="status"]')).toBeNull()
    expect(headers(host)[1].querySelector('[role="status"]')).toBeNull()
    expect(indicators(host)).toHaveLength(1)
    expect(footer(host).querySelector('[role="status"]')).not.toBeNull()
  })
  it('replaces the pending spinner with recorded duration at completion', async () => {
    const { host, render } = await mount({ events: [user], runtimeActivity: running })
    await render({ events: [user, completed], streamingText: 'Checked the project', runtimeActivity: { state: 'ready', label: 'idle' } })
    expect(headers(host)).toHaveLength(1)
    expect(indicators(host)).toHaveLength(0)
    expect(host.querySelector('[data-testid="terminal-clear-turn-done"]')?.textContent).toContain('for 32s')
    expect(host.querySelector('[data-testid="terminal-clear-live-assistant-message"]')).toBeNull()
  })
  it('shows accessible amber waiting instead of a spinner', async () => {
    const { host } = await mount({ events: [user], runtimeActivity: { state: 'waiting', label: 'waiting for input' }, assistantLabel: 'Quill' })
    expect(headers(host)[0].textContent).toContain('Quill')
    expect(footer(host).textContent).toContain('Waiting for your input')
    expect(indicators(host)[0].getAttribute('aria-label')).toBe('waiting for input')
    expect(indicators(host)[0].querySelector('.animate-spin')).toBeNull()
    expect(indicators(host)[0].querySelector('.bg-amber-400')).not.toBeNull()
  })
  it.each(['conversation_error', 'context_cancelled'])('clears activity when settled with %s', async type => {
    const { host, render } = await mount({ events: [user], runtimeActivity: running })
    await render({ events: [user, event('settled', type, { error: 'Turn stopped' })], runtimeActivity: { state: 'ready', label: 'idle' } })
    expect(indicators(host)).toHaveLength(0)
  })
  it("preserves a custom renderer's live-text cue without shared lifecycle state", async () => {
    const { host } = await mount({ events: [user], streamingText: 'Writing a reply' })
    expect(host.querySelector('[aria-label="Writing"]')).not.toBeNull()
  })
  it('keeps recorded history quiet without a live session', async () => {
    const { host } = await mount({ events: [user, completed] })
    expect(indicators(host)).toHaveLength(0)
    expect(footer(host).textContent).toBe('')
    expect(host.querySelector('[data-testid="terminal-clear-turn-done"]')?.textContent).toContain('for 32s')
  })
})

// Reproduces the real manual execute_step receipt: review runs, step does not.
it('shows the pending review truthfully without exposing a non-step inspector', async () => {
  const original = useChatStore.getState()
  cleanups.push(() => useChatStore.setState(original, true))
  const target = {session_id: 'parent', execution_id: 'review-1', workspace_path: 'Workflow/example', name: 'Workflow Review', workflow_kind: 'workflow', execution_kind: 'workflow_review', started_at: '2026-10-12T00:00:00Z'}
  const start = {...event('launch', 'tool_call_start', {tool_call_id: 'launch', tool_name: 'execute_step', tool_params: {arguments: '{"step_id":"example-step","group_name":"Test Group"}'}}), session_id: 'parent'}
  const review = {...event('review-start', 'background_agent_started', {agent_id: 'review-1', name: 'Workflow Review', execution_kind: 'sub_agent', parent_execution_id: 'main:parent', metadata: {inspection: target}}), session_id: 'parent'}
  const child = {...event('child-start', 'background_agent_started', {agent_id: 'child', parent_execution_id: 'review-1', metadata: {inspection: {...target, execution_id: 'child'}}}), session_id: 'parent'}
  const result = {...event('launch-end', 'tool_call_end', {tool_call_id: 'launch', tool_name: 'execute_step', result: 'Reviewing the workflow before running… The plan changed, so execute_step did not start yet.'}), session_id: 'parent'}
  const events = [user, start, review, child, result]
  useChatStore.setState({activeTabId: 'chat', chatTabs: {chat: {tabId: 'chat', sessionId: 'parent', metadata: {mode: 'workflow', agentProfileId: 'goals'}} as ChatTab}, tabEvents: {parent: events}})
  const {host, render} = await mount({events})
  expect(host.textContent).toContain('Reviewing workflow')
  expect(host.textContent).toContain('step waiting')
  expect(host.textContent).not.toContain('Running step')
  expect(host.querySelectorAll('[data-testid="terminal-inspectable-background-activity"]')).toHaveLength(0)
  expect(host.querySelector('[aria-label="Inspect Workflow Review"]')).toBeNull()
  expect(useChatStore.getState().getTab('chat')?.metadata?.stepInspections).toBeUndefined()
  const done = {...event('review-done', 'background_agent_completed', {agent_id: 'review-1', parent_execution_id: 'main:parent', status: 'completed', metadata: {inspection: target}}), session_id: 'parent'}
  await render({events: [...events, done]})
  expect(host.querySelectorAll('[data-testid="terminal-inspectable-background-activity"]')).toHaveLength(0)
})

// Native CLIs can launch through their shell bridge, without an execute_step
// tool receipt. The exact server lifecycle target must be sufficient.
it.each(['query_1791824440777049000', 'synthetic-turn:steer-message-1791824440777049000', 'workflow-full-example'])('opens a server-targeted step from its lifecycle (%s)', async parentID => {
  const original = useChatStore.getState()
  cleanups.push(() => useChatStore.setState(original, true))
  const target = {session_id:'parent',execution_id:'step-run',workspace_path:'Workflow/example',step_id:'example-step',workflow_kind:'workflow',execution_kind:'workflow_step',name:'Example step',started_at:'2026-10-12T00:00:00Z'}
  const started = {...event('native-step-start','background_agent_started',{agent_id:'step-run',execution_kind:'workflow_step',parent_execution_id:parentID,metadata:{inspection:target}}),session_id:'parent'}
  const child = {...event('native-child-start','background_agent_started',{agent_id:'child',execution_kind:'agent_item',parent_execution_id:'step-run',metadata:{inspection:{...target,execution_id:'child',execution_kind:'agent_item',name:'Child task'}}}),session_id:'parent'}
  const events = [user,started,child]
  useChatStore.setState({activeTabId:'chat',chatTabs:{chat:{tabId:'chat',sessionId:'parent',metadata:{mode:'workflow',agentProfileId:'goals'}} as ChatTab},tabEvents:{parent:events}})
  const {host,render} = await mount({events})
  expect(host.querySelectorAll('[data-testid="terminal-inspectable-background-activity"]')).toHaveLength(1)
  expect(host.textContent).not.toContain('Child task')
  await act(async()=>host.querySelector<HTMLButtonElement>('[aria-label="Inspect Example step"]')!.click())
  expect(useChatStore.getState().getTab('chat')?.metadata?.stepInspections?.items).toEqual([target])
  for (const status of ['completed', 'failed']) {
    const done = {...event('native-step-end','background_agent_completed',{agent_id:'step-run',parent_execution_id:parentID,status,metadata:{inspection:target}}),session_id:'parent'}
    // Completion must stay inspectable even after the launch rolls out of history.
    await render({events:[user,done]})
    expect(host.querySelectorAll('[data-testid="terminal-inspectable-background-activity"]')).toHaveLength(1)
    expect(host.querySelector('[data-testid="terminal-inspectable-background-activity"]')?.textContent).toContain(status)
    expect(host.querySelector('[aria-label="Inspect Example step"]')).not.toBeNull()
  }
})

// PLAT-746-158 B2: capture can finish after the following reply. Both views
// must attach it to its recorded reply and read saved bytes, not today's file.
it('keeps a late file comparison on its original reply in both transcript views after reload', async () => {
  const get = vi.spyOn(api, 'get').mockImplementation(async (_url, config) => {
    if (config?.params?.op === 'snapshot_file') return { data: new TextEncoder().encode('Saved report from the first reply').buffer } as never
    if (config?.params?.op === 'reply_card') return { data: { can_edit: false, actions: [], groups: [{ kind: 'crew_files', root: '', name: 'Other files', total_files: 2, files: [
      { path: 'report.md', status: 'M', added: 2, removed: 1 },
      { path: 'image.png', status: 'M', binary: true },
    ] }] } } as never
    return { data: { history: { latest_snapshot_id: '', can_review: false }, comparison: { snapshot: { status: 'complete' }, changes: [
      { path: 'report.md', status: 'M', added: 2, removed: 1 },
      { path: 'image.png', status: 'M', binary: true },
    ] } } } as never
  })
  const numbered = (id: string, type: string, data: Record<string, unknown>, sequence: number) => ({ ...event(id, type, data), sequence, session_id: 'files-session' })
  const first = numbered('first-reply', 'llm_generation_end', { content: 'First report is ready.' }, 2)
  const next = numbered('next-reply', 'llm_generation_end', { content: 'Second report is ready.' }, 5)
  const history = [numbered('first-user', 'user_message', { content: 'Make the first report' }, 1), first,
    numbered('next-user', 'user_message', { content: 'Make another report' }, 4), next]
  const late = numbered('saved-files', 'reply_file_changes', { snapshot_id: 'saved-first', workspace_path: 'Crew/example', start_sequence: 0, end_sequence: 3 }, 6)
  try {
    for (const clean of [false, true]) {
      const host = document.createElement('div'); document.body.appendChild(host)
      const root = createRoot(host)
      vi.stubGlobal('IS_REACT_ACT_ENVIRONMENT', true)
      const render = async (events: PollingEvent[]) => act(async () => root.render(clean
        ? <CleanConversationSurface events={events} isStreaming={false} isRestoring={false} streamingText="" />
        : <TerminalEventTranscript terminal={null} events={events} />))
      try {
        await render(history)
        expect(host.querySelector('[aria-label="Reply changes"]')).toBeNull()
        await render([...history, late])
        const card = host.querySelector('[aria-label="Reply changes"]')!
        expect(card).not.toBeNull()
        const reply = clean ? card.closest('article') : card.closest('[data-testid="terminal-clear-event-first-reply"]')
        expect(reply?.textContent).toContain('First report is ready.')
        expect(reply?.textContent).not.toContain('Second report is ready.')
        vi.mocked(openReplyChanges).mockClear()
        await act(async () => card.querySelector<HTMLButtonElement>('button')!.click())
        expect(host.querySelector('dialog')).toBeNull()
        // This fixture is a shared Run viewer: filenames stay readable inline,
        // and the request remains bound to the first reply's saved snapshot.
        expect(card.querySelector('[aria-label="Reply file names"]')?.textContent).toContain('report.md')
        expect(openReplyChanges).not.toHaveBeenCalled()
        expect(get).toHaveBeenCalledWith('/api/workspace-snapshots', expect.objectContaining({ params: expect.objectContaining({ snapshot_id: 'saved-first', session_id: 'files-session' }) }))
        // PLAT-746-158: a saved card disappears when the project is Cowork,
        // including after reopening old history; it does not fetch comparisons.
        const priorTabs = useChatStore.getState().chatTabs
        try {
          await act(async () => {
            useChatStore.setState({ chatTabs: { ...priorTabs, 'files-tab': { sessionId: 'files-session', metadata: { agentProfileProjectId: 'snapshot-project' } } as ChatTab } })
            setProjectMode('snapshot-project', 'cowork')
          })
          const requests = get.mock.calls.length
          await render([...history, late])
          expect(host.querySelector('[aria-label="Reply changes"]')).toBeNull()
          expect(get.mock.calls.length).toBe(requests)
          await act(async () => setProjectMode('snapshot-project', 'dev'))
          expect(host.querySelector('[aria-label="Reply changes"]')).not.toBeNull()
        } finally { await act(async () => useChatStore.setState({ chatTabs: priorTabs })) }

      } finally { await act(async () => root.unmount()); host.remove() }
    }
  } finally { get.mockRestore() }
})

// PLAT-746-158: partial captures with no observed changes used to disappear,
// making missed files indistinguishable from a successful empty comparison.
it('keeps omission notices out of the reply card in both views', async () => {
  let status = 'partial'
  let uncaptured = 1
  const get = vi.spyOn(api, 'get').mockImplementation(async () => ({ data: { history: { latest_snapshot_id: '', can_review: false }, comparison: { snapshot: { status }, uncaptured_changes: uncaptured, changes: [] } } }) as never)
  const history = [
    { ...event('user-files', 'user_message', { content: 'Change a source file' }), sequence: 1, session_id: 'files-session' },
    { ...event('reply-files', 'llm_generation_end', { content: 'Done.' }), sequence: 2, session_id: 'files-session' },
  ]
  try {
    for (const clean of [false, true]) {
      const host = document.createElement('div'); document.body.appendChild(host)
      const root = createRoot(host)
      try {
        for (const state of ['partial', 'unchanged-skips', 'complete']) {
          status = state === 'complete' ? 'complete' : 'partial'
          uncaptured = state === 'partial' ? 1 : 0
          const events = [...history, { ...event('saved-files', 'reply_file_changes', { snapshot_id: state, workspace_path: 'Crew/example', start_sequence: 0, end_sequence: 3 }), sequence: 4, session_id: 'files-session' }]
          await act(async () => root.render(clean
            ? <CleanConversationSurface events={events} isStreaming={false} isRestoring={false} streamingText="" />
            : <TerminalEventTranscript terminal={null} events={events} />))
          expect(host.textContent?.includes('may have changed but could not be compared')).toBe(false)
          expect(host.querySelector('[aria-label="Reply changes"]')).toBeNull()
        }
      } finally { await act(async () => root.unmount()); host.remove() }
    }
  } finally { get.mockRestore() }
})
