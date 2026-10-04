// @vitest-environment happy-dom
import React, { act } from 'react'
import { createRoot } from 'react-dom/client'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { TerminalEventTranscript } from './TerminalEventTranscript'
import type { PollingEvent } from '../services/api-types'

vi.mock('react-virtuoso', () => ({
  Virtuoso: ({ data, firstItemIndex, itemContent }: { data: Array<{ key: string }>; firstItemIndex: number; itemContent: (index: number, item: unknown) => React.ReactNode }) =>
    <div>{data.map((item, index) => <div key={item.key}>{itemContent(firstItemIndex + index, item)}</div>)}</div>,
}))

const cleanups: Array<() => void> = []
afterEach(() => { cleanups.splice(0).forEach(cleanup => cleanup()); vi.unstubAllGlobals() })
function event(id: string, data: Record<string, unknown>): PollingEvent {
  return { id, type: 'coding_agent_question', timestamp: '2026-10-04T14:26:40Z', data: { type: 'coding_agent_question', data } } as PollingEvent
}
const questions = [
  { id: 'scope', question: 'Choose scope', options: [{ label: 'Sheets and Gmail', description: 'Keep both integrations' }, { label: 'Gmail only' }] },
  { id: 'purpose', question: 'Choose purpose', options: [{ label: 'Read mail' }, { label: 'Send reports' }] },
]
const request = (provider = 'muse-cli', prompt = 'one') => event(`requested-${prompt}`, { provider, prompt_id: prompt, kind: 'requested', questions })
const settle = (provider = 'muse-cli', prompt = 'one') => event(`settled-${prompt}`, { provider, prompt_id: prompt, kind: 'settled', outcome: 'answered', answers: [
  { id: 'scope', selected_labels: ['Gmail only'] }, { id: 'purpose', selected_labels: ['Read mail'] },
] })
async function mount(initial: PollingEvent[], onAnswer = vi.fn().mockResolvedValue(undefined)) {
  vi.stubGlobal('IS_REACT_ACT_ENVIRONMENT', true)
  const host = document.createElement('div'); document.body.appendChild(host)
  const root = createRoot(host)
  const render = async (events: PollingEvent[], answer = onAnswer) => {
    await act(async () => root.render(<TerminalEventTranscript events={events} terminal={null} onAnswerCodingAgentQuestion={answer} />))
  }
  await render(initial)
  cleanups.push(() => { act(() => root.unmount()); host.remove() })
  return { host, render, onAnswer }
}
function button(host: Element, label: string) { return Array.from(host.querySelectorAll('button')).find(b => b.textContent === label)! }
async function choose(host: Element, value: string) {
  await act(async () => (host.querySelector(`input[value="${value}"]`) as HTMLInputElement).click())
}

describe('workflow clarification controls', () => {
  it.each(['muse-cli', 'claude-code', 'codex-cli'])('submits both questions together for %s', async provider => {
    const { host, onAnswer, render } = await mount([request(provider)])
    expect(host.textContent).toContain('Clarification needed')
    expect(host.textContent).toContain('Keep both integrations')
    expect(host.textContent).not.toMatch(/Muse|Claude|Codex/)
    expect(button(host, 'Send choice').disabled).toBe(true)
    await choose(host, 'Gmail only')
    expect(button(host, 'Send choice').disabled).toBe(true)
    await choose(host, 'Read mail')
    await act(async () => button(host, 'Send choice').click())
    expect(onAnswer).toHaveBeenCalledExactlyOnceWith(provider, 'one', [
      { id: 'scope', selectedLabels: ['Gmail only'] }, { id: 'purpose', selectedLabels: ['Read mail'] },
    ], false)
    expect(button(host, 'Submitting…').disabled).toBe(true)
    await render([request(provider), settle(provider)])
    expect(host.querySelectorAll('[data-testid="coding-agent-question-card"]')).toHaveLength(1)
    expect(host.textContent).toContain('Clarification answered')
    expect(host.querySelectorAll('input:checked')).toHaveLength(2)
    expect(host.querySelector('input:not(:disabled)')).toBeNull()
  })

  it('keeps successive prompts separate even when their question IDs recur', async () => {
    const { host, render, onAnswer } = await mount([request(), settle(), request('muse-cli', 'two')])
    const cards = host.querySelectorAll('[data-testid="coding-agent-question-card"]')
    expect(cards).toHaveLength(2)
    expect(cards[0].querySelectorAll('input:checked')).toHaveLength(2)
    expect(cards[1].querySelectorAll('input:checked')).toHaveLength(0)
    await choose(cards[1], 'Sheets and Gmail'); await choose(cards[1], 'Send reports')
    await act(async () => button(cards[1], 'Send choice').click())
    expect(onAnswer).toHaveBeenCalledWith('muse-cli', 'two', [
      { id: 'scope', selectedLabels: ['Sheets and Gmail'] }, { id: 'purpose', selectedLabels: ['Send reports'] },
    ], false)
    await render([request(), settle(), request('muse-cli', 'two'), event('settled-two', { provider: 'muse-cli', prompt_id: 'two', kind: 'settled', outcome: 'interrupted' })])
    expect(host.textContent).toContain('Clarification closed')
    expect(host.querySelectorAll('input:checked')).toHaveLength(2)
  })

  it('retains selections and permits retry after a submission failure', async () => {
    const onAnswer = vi.fn().mockRejectedValueOnce(new Error('Connection lost')).mockResolvedValue(undefined)
    const { host } = await mount([request()], onAnswer)
    await choose(host, 'Gmail only'); await choose(host, 'Read mail')
    await act(async () => button(host, 'Send choice').click())
    expect(host.querySelector('[role="alert"]')?.textContent).toBe('Connection lost')
    expect(host.querySelectorAll('input:checked')).toHaveLength(2)
    await act(async () => button(host, 'Send choice').click())
    expect(onAnswer).toHaveBeenCalledTimes(2)
  })

  it('submits a custom native Claude answer and preserves it in history', async () => {
    const requested = event('claude-other-request', { provider: 'claude-code', prompt_id: 'native', kind: 'requested', questions: [{ ...questions[0], allow_other: true }] })
    const { host, onAnswer, render } = await mount([requested])
    const other = Array.from(host.querySelectorAll('label')).find(label => label.textContent === 'Other')!
    await act(async () => (other.querySelector('input') as HTMLInputElement).click())
    expect(button(host, 'Send choice').disabled).toBe(true)
    const input = host.querySelector('input[placeholder="Type your answer"]') as HTMLInputElement
    await act(async () => {
      Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!.call(input, 'Keep both and add alerts')
      input.dispatchEvent(new Event('input', { bubbles: true }))
    })
    expect(button(host, 'Send choice').disabled).toBe(false)
    await act(async () => button(host, 'Send choice').click())
    expect(onAnswer).toHaveBeenCalledWith('claude-code', 'native', [{ id: 'scope', selectedLabels: [], otherText: 'Keep both and add alerts' }], false)
    await render([requested, event('claude-other-settle', { provider: 'claude-code', prompt_id: 'native', kind: 'settled', outcome: 'answered', answers: [{ id: 'scope', other_text: 'Keep both and add alerts' }] })])
    expect(host.textContent).toContain('Keep both and add alerts')
    expect(host.querySelector('input:checked:disabled')).not.toBeNull()
    expect(host.querySelector('input[placeholder="Type your answer"]')).toBeNull()
  })

  it('counts a custom answer towards multi-select bounds and clears it when choosing a radio option', async () => {
    const requested = event('other-bounds', { provider: 'claude-code', prompt_id: 'bounds', kind: 'requested', questions: [{ ...questions[0], allow_other: true, multi_select: true, max_selections: 1 }] })
    const { host, render, onAnswer } = await mount([requested])
    const other = Array.from(host.querySelectorAll('label')).find(label => label.textContent === 'Other')!
    await act(async () => (other.querySelector('input') as HTMLInputElement).click())
    expect((host.querySelector('input[value="Gmail only"]') as HTMLInputElement).disabled).toBe(true)
    await render([event('other-radio', { provider: 'claude-code', prompt_id: 'radio', kind: 'requested', questions: [{ ...questions[0], allow_other: true }] })])
    const radioOther = Array.from(host.querySelectorAll('label')).find(label => label.textContent === 'Other')!
    await act(async () => (radioOther.querySelector('input') as HTMLInputElement).click())
    const custom = host.querySelector('input[placeholder="Type your answer"]') as HTMLInputElement
    await act(async () => {
      Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!.call(custom, 'Keep both')
      custom.dispatchEvent(new Event('input', { bubbles: true }))
    })
    await choose(host, 'Gmail only')
    expect(host.querySelector('input[placeholder="Type your answer"]')).toBeNull()
    await act(async () => button(host, 'Send choice').click())
    expect(onAnswer).toHaveBeenCalledWith('claude-code', 'radio', [{ id: 'scope', selectedLabels: ['Gmail only'] }], false)
  })

  it('renders a pending prompt without an answer handler as read-only', async () => {
    const host = document.createElement('div'); document.body.appendChild(host)
    const root = createRoot(host)
    cleanups.push(() => { act(() => root.unmount()); host.remove() })
    await act(async () => root.render(<TerminalEventTranscript events={[request()]} terminal={null} />))
    expect(host.textContent).toContain('This conversation is read-only.')
    expect(button(host, 'Use first options').disabled).toBe(true)
    expect(host.querySelector('fieldset')?.disabled).toBe(true)
  })
})
