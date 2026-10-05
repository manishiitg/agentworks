// @vitest-environment happy-dom
import React, { act } from 'react'
import { createRoot } from 'react-dom/client'
import { afterEach, expect, it, vi } from 'vitest'
import { TerminalEventTranscript } from './TerminalEventTranscript'
import type { PollingEvent } from '../services/api-types'

vi.mock('react-virtuoso', () => ({
  Virtuoso: ({ data, components, context, itemContent, firstItemIndex }: {
    data: unknown[]; components: { Header: React.ComponentType<{ context: unknown }> }; context: unknown
    itemContent: (index: number, item: unknown) => React.ReactNode; firstItemIndex: number
  }) => <div data-testid="virtual-scroller">
    <components.Header context={context} />
    {data.map((item, index) => <div key={index}>{itemContent(firstItemIndex + index, item)}</div>)}
  </div>,
}))
vi.mock('./events/EventDispatcher', () => ({ EventDispatcher: () => null }))
vi.mock('./ui/MarkdownRenderer', () => ({ ConversationMarkdownRenderer: ({ content }: { content: string }) => <div>{content}</div> }))
const events = [{ id: 'answer', type: 'unified_completion', data: { type: 'unified_completion', data: { final_result: 'Done' } } }] as PollingEvent[]
const cleanups: (() => void)[] = []
afterEach(() => { cleanups.splice(0).forEach(fn => fn()); vi.unstubAllGlobals() })

it('keeps earlier-history navigation inside the scroller across main/run switches without fetching automatically', () => {
  vi.stubGlobal('IS_REACT_ACT_ENVIRONMENT', true)
  const host = document.createElement('div')
  const root = createRoot(host)
  cleanups.push(() => act(() => root.unmount()))
  const load = vi.fn()
  for (const key of ['main:chat', 'run:schedule', 'main:chat']) {
    act(() => root.render(<TerminalEventTranscript terminal={null} events={events} scrollKey={key} hasOlder onLoadOlder={load} />))
    const header = host.querySelector('[data-testid="transcript-history-header"]')
    expect(header?.closest('[data-testid="virtual-scroller"]')).not.toBeNull()
    expect(host.querySelector('[data-testid="terminal-clear-view"]')?.firstElementChild?.getAttribute('data-testid')).toBe('virtual-scroller')
  }
  expect(load).not.toHaveBeenCalled()
  act(() => host.querySelector<HTMLButtonElement>('[data-testid="transcript-history-header"] button')!.click())
  expect(load).toHaveBeenCalledOnce()
  act(() => root.render(<TerminalEventTranscript terminal={null} events={events} hasOlder={false} onLoadOlder={load} />))
  expect(host.querySelector('[data-testid="transcript-history-header"]')).toBeNull()
})
