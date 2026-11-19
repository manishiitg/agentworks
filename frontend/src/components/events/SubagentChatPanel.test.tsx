// @vitest-environment happy-dom
import { afterEach, expect, it, vi } from 'vitest'
import { act, createElement } from 'react'
import { createRoot } from 'react-dom/client'
import { useSubagentChat } from './SubagentChatPanel'

const { get } = vi.hoisted(() => ({ get: vi.fn() }))
vi.mock('../../services/api', async importOriginal => {
  const actual = await importOriginal<typeof import('../../services/api')>()
  return { ...actual, agentApi: { ...actual.agentApi, getSessionSubagent: get } }
})

;(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true
afterEach(() => { vi.useRealTimers(); get.mockReset(); document.body.innerHTML = '' })

const transcript = (status: string) => ({ session_id: 's1', agent_id: 'claude-code:a', name: 'Explore', status, started_at: '2026-11-18T10:00:00Z', events: [] })

it('reads a running subagent again every 2 s and stops once it finished (#3132)', async () => {
  vi.useFakeTimers()
  get
    .mockResolvedValueOnce({ running: true, transcript: transcript('running') })
    .mockResolvedValueOnce({ running: false, transcript: transcript('completed') })
  const seen: Array<boolean | undefined> = []
  const Probe = () => { const { data } = useSubagentChat('s1', 'toolu_1'); seen.push(data?.running); return null }
  const host = document.createElement('div'); document.body.append(host)
  const root = createRoot(host)
  await act(async () => { root.render(createElement(Probe)) })
  expect(get).toHaveBeenCalledTimes(1)
  await act(async () => { await vi.advanceTimersByTimeAsync(2000) })
  expect(get).toHaveBeenCalledTimes(2)
  await act(async () => { await vi.advanceTimersByTimeAsync(10000) })
  expect(get).toHaveBeenCalledTimes(2)
  expect(seen.at(-1)).toBe(false)
  act(() => root.unmount())
})

it('shows the server message when the conversation is gone, without retrying', async () => {
  vi.useFakeTimers()
  get.mockRejectedValue({ response: { status: 404, data: 'Not available any more.' } })
  let error = ''
  const Probe = () => { error = useSubagentChat('s1', 'toolu_1').error; return null }
  const host = document.createElement('div'); document.body.append(host)
  const root = createRoot(host)
  await act(async () => { root.render(createElement(Probe)) })
  await act(async () => { await vi.advanceTimersByTimeAsync(10000) })
  expect(get).toHaveBeenCalledTimes(1)
  expect(error).toBe('Not available any more.')
  act(() => root.unmount())
})

// The server's shape for a finished Claude helper (orchestrator.NativeSubagentChat):
// every event carries the same timestamp, the child's id is "<provider>:<id>".
const finished = {
  session_id: 's1', agent_id: 'claude-code:agent-1', name: 'Map frontend', kind: 'sub_agent', provider: 'claude-code', status: 'completed',
  started_at: '2026-11-19T07:00:00Z', completed_at: '2026-11-19T07:05:00Z',
  events: [
    { timestamp: '2026-11-19T07:05:00Z', type: 'user_message', role: 'user', text: 'Map the frontend feature screens', source_type: 'native_transcript' },
    { timestamp: '2026-11-19T07:05:00Z', type: 'tool_call', source_type: 'native_transcript', tool_call: { tool_call_id: 'toolu_r1', tool_name: 'Read', arguments: '{"file_path":"a.tsx"}', status: 'completed', result: 'file text', server_name: 'claude-code' } },
    { timestamp: '2026-11-19T07:05:00Z', type: 'assistant_message', role: 'assistant', text: 'The feature screens live in WorkFeaturePanel.', source_type: 'native_transcript' },
  ],
}

it('gives a finished helper\'s conversation a height to show in (#3245: the panel opened empty)', async () => {
  const { inspectionChat } = await import('../../utils/inspectionConversation')
  const { selectTerminalEvents } = await import('../../utils/terminalEventTranscript')
  const chat = inspectionChat({ transcript: finished, children: [] } as never)
  expect(chat.events.length).toBe(3)
  expect(selectTerminalEvents(chat.events, chat.terminal).length).toBe(3)

  // The transcript's root is flex-1 in a column: its container must be a flex
  // column, or the list has no height and draws no rows (the empty panel).
  get.mockResolvedValue({ running: false, transcript: finished })
  const { SubagentChatPanel } = await import('./SubagentChatPanel')
  const host = document.createElement('div'); document.body.append(host)
  const root = createRoot(host)
  await act(async () => { root.render(createElement(SubagentChatPanel, { sessionId: 's1', toolCallId: 'toolu_1', title: 'Map frontend', onClose: () => {} })) })
  const body = document.querySelector('[data-testid="subagent-chat-body"]')
  expect(body?.className.split(' ')).toEqual(expect.arrayContaining(['flex', 'flex-col', 'flex-1', 'min-h-0']))
  act(() => root.unmount())
})
