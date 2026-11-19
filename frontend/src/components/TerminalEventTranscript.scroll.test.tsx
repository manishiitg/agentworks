// @vitest-environment happy-dom
import React, { act, useRef } from 'react'
import { createRoot } from 'react-dom/client'
import { afterEach, expect, it, vi } from 'vitest'
import type { VirtuosoHandle } from 'react-virtuoso'
import type { PollingEvent } from '../services/api-types'
import { TerminalEventTranscript } from './TerminalEventTranscript'
import { transcriptReadingState, followTranscriptLatest, type TranscriptReadingState } from './useTranscriptScroll'

const scroll = vi.hoisted(() => ({ jump: vi.fn(), rows: [] as { key: string }[] }))
vi.mock('react-virtuoso', () => ({ Virtuoso: ({ data }: { data: { key: string }[] }) => { scroll.rows = data; return <div /> } }))
vi.mock('./useTranscriptScroll', async original => ({
  ...await original<typeof import('./useTranscriptScroll')>(),
  useTranscriptScroll: () => ({ following: false, settled: true, scrollerRef: vi.fn(), layoutChanged: vi.fn(), jumpToLatest: scroll.jump, jumpToKey: vi.fn(), preserveDisclosure: vi.fn(), preserveReadingPosition: vi.fn() }),
}))
// Rendering rich row bodies is independent of deciding whether history may scroll.
vi.mock('./events/EventDispatcher', () => ({ EventDispatcher: () => null }))
vi.mock('./ui/MarkdownRenderer', () => ({ ConversationMarkdownRenderer: () => null }))

const host = document.createElement('div')
const root = createRoot(host)
afterEach(() => { act(() => root.render(null)); scroll.jump.mockClear() })
const message = (id: string, sequence: number): PollingEvent => ({
  id, type: 'user_message', session_id: 'cached-chat', sequence,
  timestamp: '2026-11-14T04:00:00Z', data: { type: 'user_message', data: { content: id } },
} as PollingEvent)

// PLAT-746: reopening a cached chat refreshes its tail; a newly discovered
// historical user row must not be mistaken for the reader pressing Send.
it('keeps the reading position when cached history gains newer messages', async () => {
  const key = 'cached-chat-scroll-regression'
  const saved = transcriptReadingState(key)
  saved.following = false
  saved.deliberate = true
  saved.anchor = { key: 'previous-question', offset: 18 }
  const cached = [message('previous-question', 1)]
  await act(async () => root.render(<TerminalEventTranscript terminal={null} events={cached} scrollKey={key} />))
  expect(scroll.rows.some(row => row.key === 'previous-question')).toBe(true)
  await act(async () => root.render(<TerminalEventTranscript terminal={null} events={[...cached, message('unread-question', 2)]} scrollKey={key} />))
  expect(scroll.rows.some(row => row.key === 'unread-question')).toBe(true)
  expect(scroll.jump).not.toHaveBeenCalled()
  expect(saved.anchor).toEqual({ key: 'previous-question', offset: 18 })
  expect(saved.following).toBe(false)
})

it('still follows an explicit composer send only in its own conversation', async () => {
  const actual = await vi.importActual<typeof import('./useTranscriptScroll')>('./useTranscriptScroll')
  const saved: TranscriptReadingState = { following: false, deliberate: true, anchor: { key: 'old', offset: 18 }, disclosures: new Map() }
  function Reader() {
    const ref = useRef<VirtuosoHandle | null>(null)
    const view = actual.useTranscriptScroll(['old'], saved, ref, 'composer-chat')
    return <div data-following={String(view.following)} />
  }
  await act(async () => root.render(<Reader />))
  await act(async () => followTranscriptLatest('another-chat'))
  expect(host.firstElementChild?.getAttribute('data-following')).toBe('false')
  await act(async () => followTranscriptLatest('composer-chat'))
  expect(host.firstElementChild?.getAttribute('data-following')).toBe('true')
  expect(saved.anchor).toBeUndefined()
})

// PLAT-527: a chat opened before its history was loaded was drawn at the top
// and then jumped to the end in steps. It stays invisible until it rests there.
it('shows a newly opened transcript only once it rests at its starting position', async () => {
  const actual = await vi.importActual<typeof import('./useTranscriptScroll')>('./useTranscriptScroll')
  if (!('ResizeObserver' in window)) vi.stubGlobal('ResizeObserver', class { observe() {} disconnect() {} })
  const saved: TranscriptReadingState = { following: true, disclosures: new Map() }
  const scroller = document.createElement('div')
  let top = 0
  Object.defineProperty(scroller, 'scrollTop', { get: () => top, set: (value: number) => { top = value } })
  Object.defineProperty(scroller, 'scrollHeight', { get: () => 1500 })
  Object.defineProperty(scroller, 'clientHeight', { get: () => 500 })
  function Reader() {
    const ref = useRef<VirtuosoHandle | null>(null)
    const view = actual.useTranscriptScroll(['a', 'b'], saved, ref)
    const attach = view.scrollerRef
    return <div ref={() => attach(scroller)} data-positioned={String(view.positioned)} />
  }
  const wait = (ms: number) => act(async () => { await new Promise(resolve => setTimeout(resolve, ms)) })
  await act(async () => root.render(<Reader />))
  await wait(150)
  expect(host.firstElementChild?.getAttribute('data-positioned')).toBe('false')
  top = 1000
  await wait(150)
  expect(host.firstElementChild?.getAttribute('data-positioned')).toBe('true')
})
