// @vitest-environment happy-dom
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { afterEach, describe, expect, it, vi } from 'vitest'
import type { VirtuosoHandle } from 'react-virtuoso'
import { TRANSCRIPT_SETTLE_MS, followTranscriptLatest, transcriptBlank, transcriptIsFollowing, transcriptReadingState, useTranscriptScroll, type TranscriptReadingState } from './useTranscriptScroll'

const cleanups: Array<() => void> = []
afterEach(() => { cleanups.splice(0).forEach(cleanup => cleanup()); vi.unstubAllGlobals() })

function mountTranscript() {
  vi.stubGlobal('IS_REACT_ACT_ENVIRONMENT', true)
  const frames = new Map<number, FrameRequestCallback>()
  let frameId = 0
  vi.stubGlobal('requestAnimationFrame', (callback: FrameRequestCallback) => { frames.set(++frameId, callback); return frameId })
  vi.stubGlobal('cancelAnimationFrame', (id: number) => frames.delete(id))
  const element = document.createElement('div')
  let height = 1000
  Object.defineProperties(element, { scrollHeight: { get: () => height }, clientHeight: { value: 500 } })
  element.scrollTop = 500
  document.body.append(element)
  const host = document.createElement('div')
  document.body.append(host)
  const root = createRoot(host)
  const scrollTo = vi.fn(({ top }: { top: number }) => { element.scrollTop = top })
  const virtuoso = { current: { scrollTo, scrollToIndex: vi.fn() } as unknown as VirtuosoHandle }
  const saved: TranscriptReadingState = { following: true, disclosures: new Map() }
  let hook!: ReturnType<typeof useTranscriptScroll>
  function Harness() { hook = useTranscriptScroll([], saved, virtuoso, 'tab-1'); return null }
  act(() => root.render(<Harness />))
  const flush = () => act(() => { const pending = [...frames.values()]; frames.clear(); pending.forEach(callback => callback(0)) })
  cleanups.push(() => { act(() => root.unmount()); element.remove(); host.remove() })
  return { element, scrollTo, frames, flush, saved, get hook() { return hook }, grow: () => { height += 100 }, shrink: () => { height -= 100 } }
}

describe('transcript scroll DOM lifecycle', () => {
  it('attaches after hydration, follows growth, and stops issuing movements at the physical end', () => {
    const test = mountTranscript()
    act(() => test.hook.scrollerRef(test.element))
    test.flush()
    expect(test.scrollTo).not.toHaveBeenCalled()
    test.grow()
    act(() => test.hook.layoutChanged())
    test.flush()
    expect(test.scrollTo).toHaveBeenCalledExactlyOnceWith({ top: 600, behavior: 'auto' })
    for (let i = 0; i < 5; i++) { act(() => test.hook.layoutChanged()); test.flush() }
    expect(test.scrollTo).toHaveBeenCalledTimes(1)
  })

  it('lets upward input cancel a queued follow and stays paused during later output', () => {
    const test = mountTranscript()
    act(() => test.hook.scrollerRef(test.element))
    test.flush()
    test.grow()
    act(() => test.hook.layoutChanged())
    const wheel = new WheelEvent('wheel', { deltaY: -100, cancelable: true })
    act(() => test.element.dispatchEvent(wheel))
    expect(wheel.defaultPrevented).toBe(false)
    test.flush()
    expect(test.scrollTo).not.toHaveBeenCalled()
    expect(test.saved.following).toBe(false)
    act(() => test.hook.layoutChanged())
    test.flush()
    expect(test.scrollTo).not.toHaveBeenCalled()
    act(() => test.hook.jumpToLatest())
    test.flush()
    expect(test.element.scrollTop).toBe(600)
    expect(test.saved.following).toBe(true)
  })

  it('pauses for keyboard reading but leaves nested output scrolling native', () => {
    const test = mountTranscript()
    act(() => test.hook.scrollerRef(test.element))
    test.flush()
    const output = document.createElement('pre')
    output.style.overflowY = 'auto'
    output.scrollTop = 100
    Object.defineProperties(output, { scrollHeight: { value: 1000 }, clientHeight: { value: 200 } })
    test.element.append(output)
    const wheel = new WheelEvent('wheel', { deltaY: -50, bubbles: true, cancelable: true })
    act(() => output.dispatchEvent(wheel))
    expect(wheel.defaultPrevented).toBe(false)
    expect(test.saved.following).toBe(true)
    act(() => test.element.dispatchEvent(new KeyboardEvent('keydown', { key: 'PageUp' })))
    expect(test.saved.following).toBe(false)
  })

  it('a wheel down at the bottom does not later read a layout shrink as reading upward', () => {
    const test = mountTranscript()
    act(() => test.hook.scrollerRef(test.element))
    test.flush()
    // Already at the end: the gesture moves nothing.
    act(() => test.element.dispatchEvent(new WheelEvent('wheel', { deltaY: 100 })))
    // A sent message is replaced; the list briefly shrinks and the browser
    // clamps scrollTop.
    test.shrink()
    test.element.scrollTop = 400
    act(() => test.element.dispatchEvent(new Event('scroll')))
    expect(test.saved.following).toBe(true)
    test.grow(); test.grow()
    act(() => test.hook.layoutChanged())
    test.flush()
    expect(test.element.scrollTop).toBe(600)
  })

  it('sending a message follows the bottom again even after the reader scrolled up', () => {
    const test = mountTranscript()
    act(() => test.hook.scrollerRef(test.element))
    test.flush()
    act(() => test.element.dispatchEvent(new WheelEvent('wheel', { deltaY: -100 })))
    expect(test.saved.following).toBe(false)
    followTranscriptLatest('other-tab')
    expect(test.saved.following).toBe(false)
    test.grow()
    act(() => followTranscriptLatest('tab-1'))
    test.flush()
    expect(test.saved.following).toBe(true)
    expect(test.element.scrollTop).toBe(600)
  })
})

describe('transcriptBlank', () => {
  const box = (top: number, bottom: number) => ({ top, bottom, height: bottom - top, left: 0, right: 100, width: 100, x: 0, y: top, toJSON: () => ({}) }) as DOMRect
  const scrollerWith = (rows: [number, number][]) => {
    const scroller = document.createElement('div')
    scroller.getBoundingClientRect = () => box(100, 500)
    for (const [top, bottom] of rows) {
      const row = document.createElement('div')
      row.dataset.transcriptKey = `r${top}`
      row.getBoundingClientRect = () => box(top, bottom)
      scroller.append(row)
    }
    return scroller
  }
  it('is blank when every drawn row is outside the visible area (the send-then-blank case)', () => {
    expect(transcriptBlank(scrollerWith([[-900, -600], [-600, -300]]))).toBe(true)
    expect(transcriptBlank(scrollerWith([[600, 800]]))).toBe(true)
  })
  it('is not blank when any row is visible', () => {
    expect(transcriptBlank(scrollerWith([[-300, 150]]))).toBe(false)
    expect(transcriptBlank(scrollerWith([[200, 300]]))).toBe(false)
  })
})

describe('reading position is saved only when deliberate', () => {
  it('a scroll reported while the list is still mounting is not saved as a reading position', async () => {
    vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout'] })
    try {
      const test = mountTranscript()
      act(() => test.hook.scrollerRef(test.element))
      test.element.scrollTop = 0
      const row = document.createElement('div')
      row.dataset.transcriptKey = 'row-0'
      test.element.append(row)
      act(() => { test.element.dispatchEvent(new Event('scroll')) })
      act(() => test.hook.scrollerRef(null))
      expect(test.saved.anchor).toBeUndefined()
      expect(test.saved.following).toBe(true)
      expect(test.saved.deliberate).not.toBe(true)
    } finally { vi.useRealTimers() }
  })

  it('a wheel-up during mounting is a deliberate scroll-up and is kept', () => {
    vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout'] })
    try {
      const test = mountTranscript()
      act(() => test.hook.scrollerRef(test.element))
      act(() => { test.element.dispatchEvent(new WheelEvent('wheel', { deltaY: -100 })) })
      expect(test.saved.following).toBe(false)
      expect(test.saved.deliberate).toBe(true)
    } finally { vi.useRealTimers() }
  })

  it('after the settle window scroll positions are remembered', () => {
    vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout'] })
    try {
      const test = mountTranscript()
      act(() => test.hook.scrollerRef(test.element))
      const row = document.createElement('div')
      row.dataset.transcriptKey = 'row-9'
      row.getBoundingClientRect = () => ({ top: 0, bottom: 100, left: 0, right: 0, width: 0, height: 100 } as DOMRect)
      test.element.append(row)
      act(() => { vi.advanceTimersByTime(TRANSCRIPT_SETTLE_MS + 1) })
      act(() => { test.element.dispatchEvent(new Event('scroll')) })
      expect(test.saved.anchor?.key).toBe('row-9')
      expect(test.hook.settled).toBe(true)
    } finally { vi.useRealTimers() }
  })

  it('sending a message clears a deliberate reading position', () => {
    const test = mountTranscript()
    act(() => test.hook.scrollerRef(test.element))
    act(() => { test.element.dispatchEvent(new WheelEvent('wheel', { deltaY: -100 })) })
    act(() => test.hook.jumpToLatest())
    expect(test.saved.deliberate).toBe(false)
    expect(test.saved.following).toBe(true)
  })
})

describe('saved reading state across fast switching', () => {
  it('A->B->A->B never leaves a bogus top state; a deliberate scroll-up is kept', () => {
    const a = transcriptReadingState('chat-a')
    // A is mid-mount and leaves a bogus "reading at the top" state behind.
    a.following = false
    a.anchor = { key: 'row-0', offset: 0 }
    const b = transcriptReadingState('chat-b')
    expect(transcriptReadingState('chat-a')).toBe(a)
    expect(a.following).toBe(true)
    expect(a.anchor).toBeUndefined()
    expect(transcriptReadingState('chat-b')).toBe(b)
    expect(transcriptReadingState('chat-a').following).toBe(true)
    expect(transcriptReadingState('chat-b').following).toBe(true)
    expect(transcriptIsFollowing('chat-a')).toBe(true)
    // B's reader scrolled up on purpose: honoured on every later visit.
    b.following = false
    b.deliberate = true
    b.anchor = { key: 'row-7', offset: 12 }
    for (let visit = 0; visit < 3; visit++) {
      transcriptReadingState('chat-a')
      const again = transcriptReadingState('chat-b')
      expect(again.following).toBe(false)
      expect(again.anchor).toEqual({ key: 'row-7', offset: 12 })
    }
    expect(transcriptIsFollowing('chat-b')).toBe(false)
    // A running-turn chat with no deliberate state opens at the bottom and follows.
    expect(transcriptReadingState('chat-running')).toMatchObject({ following: true, anchor: undefined })
  })
})
