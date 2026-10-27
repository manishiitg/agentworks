// @vitest-environment happy-dom
import React, { act, useState } from 'react'
import { createRoot } from 'react-dom/client'
import { expect, it, vi } from 'vitest'
import type { PollingEvent } from '../../services/api-types'
import { TerminalEventTranscript } from '../TerminalEventTranscript'
import { findTranscriptMatches, transcriptSearchText } from '../../utils/transcriptSearch'

const scrolling = vi.hoisted(() => ({ scrollToIndex: vi.fn() }))
vi.mock('react-virtuoso', async () => {
  const React = await import('react')
  return { Virtuoso: React.forwardRef(({ data, firstItemIndex, itemContent }: { data: unknown[]; firstItemIndex: number; itemContent: (index: number, item: unknown) => React.ReactNode }, ref) => {
    React.useImperativeHandle(ref, () => scrolling)
    return <div>{data.map((item, index) => <div key={index}>{itemContent(firstItemIndex + index, item)}</div>)}</div>
  }) }
})
vi.mock('../events/EventDispatcher', () => ({ EventDispatcher: () => null }))
vi.mock('../ui/MarkdownRenderer', () => ({ ConversationMarkdownRenderer: ({ content }: { content: string }) => <div>{content}</div> }))

function event(id: string, type: string, data: Record<string, unknown>): PollingEvent {
  return { id, type, timestamp: '2026-10-28T00:00:00Z', data: { type, data } } as PollingEvent
}

// PLAT-746-158 A3: browser find misses virtualized and folded tool rows. Search
// the selected transcript, expand its match, and retain it when history loads.
it('finds folded steps, searches older loaded messages, and returns keyboard focus on Escape', async () => {
  vi.stubGlobal('IS_REACT_ACT_ENVIRONMENT', true)
  const host = document.createElement('div'); host.dataset.chatFindScope = ''; document.body.append(host)
  const mounted = document.createElement('div'); host.append(mounted)
  const composer = document.createElement('textarea'); composer.dataset.chatComposer = ''; host.append(composer); composer.focus()
  const root = createRoot(mounted)
  const initial = [
    event('user', 'user_message', { content: 'Read the file' }),
    event('start', 'tool_call_start', { tool_call_id: 'read', tool_name: 'read_file', tool_params: { path: 'quartz.txt' } }),
    event('end', 'tool_call_end', { tool_call_id: 'read', tool_name: 'read_file', result: 'Found quartz in the file.' }),
  ]
  function View() {
    const [events, setEvents] = useState(initial)
    return <TerminalEventTranscript searchable events={events} terminal={null} hasOlder={events.length === 3}
      onLoadOlder={() => setEvents([event('older', 'user_message', { content: 'Earlier quartz note' }), ...initial])} />
  }
  try {
    await act(async () => root.render(<View />))
    const transcript = host.querySelector<HTMLElement>('[data-chat-find-root]')!
    transcript.getClientRects = () => [new DOMRect(0, 0, 600, 400)] as unknown as DOMRectList
    expect(host.querySelector('[role="search"]')).toBeNull()
    expect(host.textContent).not.toContain('Found quartz')
    // Files/dashboard controls and another editor inside the chat keep native find.
    for (const parent of [document.body, host]) {
      const editor = document.createElement('textarea'); parent.append(editor); editor.focus()
      const nativeFind = new KeyboardEvent('keydown', { key: 'f', metaKey: true, bubbles: true, cancelable: true })
      await act(async () => editor.dispatchEvent(nativeFind))
      expect(nativeFind.defaultPrevented).toBe(false)
      expect(host.querySelector('[role="search"]')).toBeNull()
      editor.remove()
    }
    const otherChat = document.createElement('div'); otherChat.dataset.chatFindScope = ''; host.append(otherChat)
    const otherButton = document.createElement('button'); otherChat.append(otherButton); otherButton.focus()
    const otherFind = new KeyboardEvent('keydown', { key: 'f', ctrlKey: true, bubbles: true, cancelable: true })
    await act(async () => otherButton.dispatchEvent(otherFind))
    expect(otherFind.defaultPrevented).toBe(false)
    otherChat.remove()
    composer.focus()
    const shortcut = new KeyboardEvent('keydown', { key: 'f', ctrlKey: true, bubbles: true, cancelable: true })
    await act(async () => composer.dispatchEvent(shortcut))
    expect(shortcut.defaultPrevented).toBe(true)
    const input = host.querySelector<HTMLInputElement>('[aria-label="Find in loaded messages"]')!
    expect(document.activeElement).toBe(input)
    await act(async () => {
      Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!.call(input, 'quartz')
      input.dispatchEvent(new Event('input', { bubbles: true }))
    })
    expect(host.querySelector('[role="search"]')?.textContent).toContain('1 of 1')
    expect(host.textContent).toContain('Found quartz')
    expect(scrolling.scrollToIndex).toHaveBeenLastCalledWith(expect.objectContaining({ index: 1 }))
    const older = Array.from(host.querySelectorAll('button')).find(button => button.textContent === 'Load older messages')!
    await act(async () => older.click())
    expect(host.querySelector('[role="search"]')?.textContent).toContain('2 of 2')
    await act(async () => host.querySelector<HTMLButtonElement>('[aria-label="Next match"]')!.click())
    expect(host.querySelector('[role="search"]')?.textContent).toContain('Earlier quartz note')
    expect(scrolling.scrollToIndex).toHaveBeenLastCalledWith(expect.objectContaining({ index: 0 }))
    await act(async () => input.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true, cancelable: true })))
    expect(host.querySelector('[role="search"]')).toBeNull()
    expect(document.activeElement).toBe(composer)
  } finally { act(() => root.unmount()); host.remove(); composer.remove(); vi.unstubAllGlobals() }
})

// Only display content is indexed; diagnostic metadata is not a second chat.
it('excludes event metadata and uses literal search text', () => {
  const text = transcriptSearchText({ kind: 'event', key: 'one', event: event('one', 'user_message', {
    content: 'a+b is literal', metadata: { secret_note: 'not-visible' },
  }) })
  expect(findTranscriptMatches([{ key: 'one', text }], 'not-visible')).toEqual([])
  expect(findTranscriptMatches([{ key: 'one', text }], 'a+b')[0]?.match).toBe('a+b')
})
