// @vitest-environment happy-dom
import React, { act } from 'react'
import { createRoot } from 'react-dom/client'
import { expect, it, vi } from 'vitest'
import type { PollingEvent } from '../services/api-types'
import { TerminalEventTranscript } from './TerminalEventTranscript'
import { useChatDisplayStore } from '../stores/useChatDisplayStore'

vi.mock('react-virtuoso', () => ({ Virtuoso: ({ data, itemContent, computeItemKey, firstItemIndex = 0 }: {
  data: { key: string }[]; firstItemIndex?: number
  itemContent: (index: number, item: { key: string }) => React.ReactNode
  computeItemKey: (index: number, item: { key: string }) => string
}) => <>{data.map((item, index) => <React.Fragment key={computeItemKey(index, item)}>{itemContent(firstItemIndex + index, item)}</React.Fragment>)}</> }))
vi.mock('./useTranscriptScroll', async original => ({
  ...await original<typeof import('./useTranscriptScroll')>(),
  useTranscriptScroll: () => ({ following: true, settled: true, scrollerRef: vi.fn(), layoutChanged: vi.fn(), jumpToLatest: vi.fn(), jumpToKey: vi.fn(), preserveDisclosure: vi.fn(), preserveReadingPosition: vi.fn() }),
}))
vi.mock('./events/EventDispatcher', () => ({ EventDispatcher: () => null }))
vi.mock('./ui/MarkdownRenderer', () => ({ ConversationMarkdownRenderer: ({ content }: { content: string }) => <span>{content}</span> }))
Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })

// PLAT-746-995: Codex reasoning hidden between tool batches left a ladder
// of separate count rows between two replies. Exercise the real chat pipeline.
it('shows one 5 steps row across hidden thinking and preserves visible boundaries and the growing tool disclosure', async () => {
  let sequence = 0
  const event = (id: string, type: string, fields: Record<string, unknown>): PollingEvent => ({
    id, type, sequence: ++sequence, session_id: 'steps-chat', timestamp: '2026-11-14T12:00:00Z', data: { type, data: fields },
  } as PollingEvent)
  const calls = (ids: string[]) => ids.flatMap(id => [
    event(id, 'tool_call_start', { tool_call_id: id, tool_name: 'exec_command', tool_params: { arguments: '{"cmd":"echo ok"}' } }),
    event(`${id}-end`, 'tool_call_end', { tool_call_id: id, tool_name: 'exec_command', result: 'ok' }),
  ])
  const before = event('before', 'llm_generation_end', { content: 'First reply' })
  const first = calls(['a', 'b'])
  const thought = event('thought', 'conversation_thinking', { thinking: 'A supplied reasoning summary' })
  const middle = calls(['c'])
  const empty = event('empty', 'conversation_thinking', { thinking: '', metadata: { presentation: 'assistant_update' } })
  const status = event('status', 'status_line', { content: 'Working' })
  const last = calls(['d', 'e'])
  const after = event('after', 'llm_generation_end', { content: 'Second reply' })
  const events = [before, ...first, thought, ...middle, empty, status, ...last, after]
  const original = JSON.stringify(events)
  const host = document.createElement('div'), root = createRoot(host)
  const render = (rows = events) => act(async () => root.render(<TerminalEventTranscript terminal={null} events={rows.map((row, index) => ({ ...row, sequence: index + 1 }))} searchable scrollKey="combined-steps-regression" />))
  try {
    useChatDisplayStore.setState({ showThinking: false, showTools: false })
    await render()
    expect([...host.querySelectorAll('[data-testid="terminal-work-summary"]')].map(row => row.textContent)).toEqual(['5 steps'])
    expect(host.textContent).toContain('First reply')
    expect(host.textContent).toContain('Second reply')
    await act(async () => useChatDisplayStore.setState({ showThinking: true }))
    expect([...host.querySelectorAll('[data-testid="terminal-work-summary"]')].map(row => row.textContent)).toEqual(['2 steps', '3 steps'])
    expect(host.textContent).toContain('A supplied reasoning summary')
    // A real message between tools must still break the run with thinking off.
    await act(async () => useChatDisplayStore.setState({ showThinking: false }))
    await render([before, ...first, event('message', 'user_message', { content: 'Please check this' }), ...middle, ...last, after])
    expect(host.querySelectorAll('[data-testid="terminal-work-summary"]')).toHaveLength(2)
    await act(async () => useChatDisplayStore.setState({ showTools: true }))
    await render([before, ...first])
    const button = host.querySelector<HTMLButtonElement>('[data-testid="terminal-tool-call-group"] button')!
    await act(async () => button.click())
    expect(button.getAttribute('aria-expanded')).toBe('true')
    await render()
    expect(host.querySelectorAll('[data-testid="terminal-tool-call-group"]')).toHaveLength(1)
    const grown = host.querySelector('[data-testid="terminal-tool-call-group"] button')!
    expect(grown.textContent).toBe('5 tool calls')
    expect(grown.getAttribute('aria-expanded')).toBe('true')
    expect(JSON.stringify(events)).toBe(original)
  } finally {
    await act(async () => root.unmount())
    useChatDisplayStore.setState({ showThinking: false, showTools: false })
  }
})

it('groups ended shells by server kind and absorbs only their exact adjacent tool wrappers (PLAT-746-995)', async () => {
  const event = (id: string, type: string, fields: Record<string, unknown>): PollingEvent => ({
    id, type, session_id: 'shell-chat', timestamp: '2026-11-14T12:00:00Z', data: { type, data: fields },
  } as PollingEvent)
  const shell = (id: string, state: string) => [
    event(`${id}-tool`, 'tool_call_end', { tool_call_id: id, tool_name: 'exec_command', result: 'private tool output' }),
    event(id, 'coding_agent_native_activity', { record: { kind: 'background_command', work_id: `work-${id}`, tool_id: id, state, command_label: `check ${id}`, started_at: '0001-01-01T00:00:00Z', ended_at: '0001-01-01T00:00:00Z' } }),
  ]
  const rows = [...shell('one', 'finished'), ...shell('two', 'failed'), ...shell('three', 'stopped'), ...shell('four', 'interrupted')]
  const host = document.createElement('div'), root = createRoot(host)
  const render = (events = rows) => act(async () => root.render(<TerminalEventTranscript terminal={null} events={events.map((row, index) => ({ ...row, sequence: index + 1 }))} searchable scrollKey="shell-groups-regression" />))
  try {
    useChatDisplayStore.setState({ showThinking: false, showTools: false })
    await render()
    expect(host.querySelectorAll('[data-testid="compact-shell-group"]')).toHaveLength(1)
    expect(host.textContent).toContain('4 background shells ended')
    expect(host.querySelector('[data-testid="terminal-work-summary"]')).toBeNull()
    const button = host.querySelector<HTMLButtonElement>('[data-testid="compact-shell-group"] button')!
    await act(async () => button.click())
    for (const state of ['finished', 'failed', 'stopped', 'interrupted']) expect(host.textContent).toContain(`Background shell ${state}`)
    expect(host.textContent).toContain('check one')
    expect(host.textContent).not.toContain('private tool output')
    expect(host.textContent).not.toContain('restart')
    await act(async () => useChatDisplayStore.setState({ showTools: true }))
    expect(host.querySelector('[data-testid="terminal-tool-call-group"]')).toBeNull()
    expect(host.textContent).toContain('exec_command')
    // An unrelated tool is still shown and breaks the shell run.
    await act(async () => useChatDisplayStore.setState({ showTools: false }))
    await render([...rows.slice(0, 2), event('other', 'tool_call_end', { tool_call_id: 'unrelated', tool_name: 'read_file', result: 'ok' }), ...rows.slice(2)])
    expect(host.querySelector('[data-testid="terminal-work-summary"]')?.textContent).toBe('2 steps')
    expect(host.textContent).toContain('3 background shells ended')
  } finally {
    await act(async () => root.unmount())
    useChatDisplayStore.setState({ showThinking: false, showTools: false })
  }
})
