// @vitest-environment happy-dom
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { afterEach, describe, expect, it, vi } from 'vitest'
import type { VirtuosoHandle } from 'react-virtuoso'
import { followTranscriptLatest, useTranscriptScroll, type TranscriptReadingState } from './useTranscriptScroll'

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
