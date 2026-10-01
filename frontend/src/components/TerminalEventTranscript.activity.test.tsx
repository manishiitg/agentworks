// @vitest-environment happy-dom
import React, { act } from 'react'
import { createRoot } from 'react-dom/client'
import { afterEach, describe, expect, it, vi } from 'vitest'
import type { PollingEvent } from '../services/api-types'
import { TerminalEventTranscript } from './TerminalEventTranscript'

vi.mock('react-virtuoso', () => ({
  Virtuoso: ({ data, firstItemIndex, itemContent, computeItemKey }: { data: unknown[]; firstItemIndex: number; itemContent: (index: number, item: unknown) => React.ReactNode; computeItemKey: (index: number, item: unknown) => string }) => (
    <div>{data.map((item, index) => <div key={computeItemKey(firstItemIndex + index, item)}>{itemContent(firstItemIndex + index, item)}</div>)}</div>
  ),
}))
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
function indicators(host: HTMLElement) { return host.querySelectorAll('[data-testid="agent-runtime-activity"]') }
function headers(host: HTMLElement) { return host.querySelectorAll('[data-testid="terminal-clear-assistant-header"]') }

describe('activity belongs to the current agent turn', () => {
  it.each([{ events: [] }, { events: [user] }])('shows the agent before any response or tool event arrives', async ({ events }) => {
    const { host } = await mount({ events, runtimeActivity: running })
    expect(headers(host)).toHaveLength(1)
    expect(indicators(host)).toHaveLength(1)
    expect(headers(host)[0].querySelector('.animate-spin')).not.toBeNull()
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
    expect(headers(host)[0].textContent).toContain('32.1s')
    expect(headers(host)[0].querySelector('[role="status"]')).toBeNull()
    expect(headers(host)[1].querySelector('[role="status"]')).not.toBeNull()
  })
  it('replaces the pending spinner with recorded duration at completion', async () => {
    const { host, render } = await mount({ events: [user], runtimeActivity: running })
    await render({ events: [user, completed], streamingText: 'Checked the project', runtimeActivity: { state: 'ready', label: 'idle' } })
    expect(headers(host)).toHaveLength(1)
    expect(indicators(host)).toHaveLength(0)
    expect(headers(host)[0].textContent).toContain('32.1s')
    expect(host.querySelector('[data-testid="terminal-clear-live-assistant-message"]')).toBeNull()
  })
  it('shows accessible amber waiting instead of a spinner', async () => {
    const { host } = await mount({ events: [user], runtimeActivity: { state: 'waiting', label: 'waiting for input' }, assistantLabel: 'Quill' })
    expect(headers(host)[0].textContent).toContain('Quill')
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
    expect(headers(host)[0].textContent).toContain('32.1s')
  })
})
