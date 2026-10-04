import { useCallback, useEffect, useRef, useState, type RefObject } from 'react'
import type { VirtuosoHandle } from 'react-virtuoso'

export interface ReadingAnchor { key: string; offset: number }
export interface TranscriptReadingState {
  following: boolean
  anchor?: ReadingAnchor
  disclosures: Map<string, boolean>
}
const positions = new Map<string, TranscriptReadingState>()
export function transcriptReadingState(key: string): TranscriptReadingState {
  const saved = positions.get(key) ?? { following: true, disclosures: new Map<string, boolean>() }
  positions.delete(key)
  positions.set(key, saved)
  if (positions.size > 40) positions.delete(positions.keys().next().value!)
  return saved
}

// Whether this conversation's transcript is pinned to the latest message (true
// for one that has no saved reading position yet).
export function transcriptIsFollowing(key: string): boolean {
  return positions.get(key)?.following ?? true
}

const FOLLOW_LATEST_EVENT = 'transcript-follow-latest'

// The user just sent a message in this conversation: show the bottom. The
// composer is not inside the transcript's scroller, so scrolling the chat
// container does nothing; the transcript itself must follow. Marking the saved
// state too covers a transcript that remounts before the message renders.
export function followTranscriptLatest(key: string | undefined) {
  if (!key) return
  const saved = positions.get(key)
  if (saved) {
    saved.following = true
    saved.anchor = undefined
  }
  window.dispatchEvent(new CustomEvent<string>(FOLLOW_LATEST_EVENT, { detail: key }))
}

// One cancellable request per frame. User input wins even if output/measurement
// scheduled an automatic movement just before the gesture.
export class TranscriptScrollController {
  private frame: number | null = null
  following: boolean
  private move: () => void
  private changed: (following: boolean) => void
  private request: (cb: FrameRequestCallback) => number
  private cancel: (id: number) => void
  constructor(
    following: boolean,
    move: () => void,
    changed: (following: boolean) => void,
    request = (cb: FrameRequestCallback) => requestAnimationFrame(cb),
    cancel = (id: number) => cancelAnimationFrame(id),
  ) {
    this.following = following
    this.move = move
    this.changed = changed
    this.request = request
    this.cancel = cancel
  }

  pause() {
    this.cancelPending()
    this.following = false
    this.changed(false)
  }
  resume() {
    this.following = true
    this.changed(true)
    this.layoutChanged()
  }
  layoutChanged() {
    if (!this.following || this.frame !== null) return
    this.frame = this.request(() => {
      this.frame = null
      if (this.following) this.move()
    })
  }
  cancelPending() {
    if (this.frame !== null) this.cancel(this.frame)
    this.frame = null
  }
}

// Maintain Virtuoso's inverse-pagination index only for a real leading-page
// change. Completion reconciliation can replace/reorder the bounded tail while
// also appending a new reply. Treating the first coincidentally surviving row
// as proof of a prepend corrupts Virtuoso's index mapping and can leave an old
// reply rendered beside the new one. A pagination prepend/removal preserves
// the existing tail; a live tail replacement does not.
//
// The first-survivor calculation still handles a tool batch that coalesces at
// the pagination boundary once that preserved-tail invariant is established.
export function prependedIndex(previous: string[], next: string[], index: number): number {
  if (previous.length === 0 || next.length === 0) return index
  if (previous.at(-1) !== next.at(-1)) return index
  const nextIndices = new Map(next.map((key, i) => [key, i]))
  for (let i = 0; i < previous.length; i++) {
    const nextIndex = nextIndices.get(previous[i])
    if (nextIndex !== undefined) return Math.max(0, index - (nextIndex - i))
  }
  return index
}

function nestedScroller(target: EventTarget | null, root: HTMLElement, delta: number): boolean {
  let element = target instanceof Element ? target : null
  while (element && element !== root) {
    if (element instanceof HTMLElement && /auto|scroll/.test(getComputedStyle(element).overflowY)) {
      const remaining = element.scrollHeight - element.clientHeight - element.scrollTop
      if (delta < 0 ? element.scrollTop > 0 : remaining > 1) return true
    }
    element = element.parentElement
  }
  return false
}

/**
 * Whether the transcript shows no message row although it has some: every row Virtuoso drew
 * is outside the visible part of the scroller. A list with no rows at all is not blank.
 */
export function transcriptBlank(scroller: HTMLElement): boolean {
  const rows = scroller.querySelectorAll<HTMLElement>('[data-transcript-key]')
  if (rows.length === 0) return scroller.scrollHeight > scroller.clientHeight + 1
  const view = scroller.getBoundingClientRect()
  if (view.height <= 0) return false
  for (const row of Array.from(rows)) {
    const box = row.getBoundingClientRect()
    if (box.bottom > view.top + 1 && box.top < view.bottom - 1) return false
  }
  return true
}

export function useTranscriptScroll(
  keys: string[],
  saved: TranscriptReadingState,
  virtuoso: RefObject<VirtuosoHandle | null>,
  readingKey?: string,
) {
  const [scroller, setScroller] = useState<HTMLElement | null>(null)
  const scrollerElement = useRef<HTMLElement | null>(null)
  const [following, setFollowing] = useState(saved.following)
  const currentKeys = useRef(keys)
  const focusAnchor = useRef<ReadingAnchor | undefined>(undefined)
  const restoreFrame = useRef<number | null>(null)
  const mounted = useRef(false)
  const blankCheck = useRef<number | null>(null)
  const [controller] = useState(() => new TranscriptScrollController(
    saved.following,
    () => {
      const element = scrollerElement.current
      if (!element) return
      const bottom = Math.max(0, element.scrollHeight - element.clientHeight)
      // Stop once we reach the physical end. scrollToIndex retries while rows
      // are measured; starting that process on each resize can fight itself.
      if (bottom - element.scrollTop > 1) virtuoso.current?.scrollTo({ top: bottom, behavior: 'auto' })
      // Blank-transcript recovery: the bottom is read before the list re-measures a new or
      // replaced row, so the scroller can land where Virtuoso drew no rows. Virtuoso draws
      // rows a frame or two after a scroll, so only act on a blank that persists, once at a
      // time, and only while still following the latest message.
      if (blankCheck.current !== null) window.clearTimeout(blankCheck.current)
      blankCheck.current = window.setTimeout(() => {
        blankCheck.current = null
        if (!saved.following || !transcriptBlank(element)) return
        virtuoso.current?.scrollToIndex({ index: 'LAST', align: 'end', behavior: 'auto' })
      }, 350)
    },
    (value) => { saved.following = value; setFollowing(value) },
  ))
  useEffect(() => { currentKeys.current = keys }, [keys])
  const cancelRestore = useCallback(() => {
    if (restoreFrame.current !== null) cancelAnimationFrame(restoreFrame.current)
    restoreFrame.current = null
  }, [])
  const pause = useCallback(() => {
    focusAnchor.current = undefined
    cancelRestore()
    controller.pause()
  }, [cancelRestore, controller])
  const jumpToLatest = useCallback(() => {
    focusAnchor.current = undefined
    cancelRestore()
    controller.resume()
  }, [cancelRestore, controller])
  const layoutChanged = useCallback(() => {
    controller.layoutChanged()
    const anchor = focusAnchor.current
    if (controller.following || !anchor || restoreFrame.current !== null) return
    restoreFrame.current = requestAnimationFrame(() => {
      restoreFrame.current = null
      if (focusAnchor.current !== anchor || controller.following) return
      const index = currentKeys.current.indexOf(anchor.key)
      if (index < 0) return
      const element = scrollerElement.current
      const row = element && Array.from(element.querySelectorAll<HTMLElement>('[data-transcript-key]'))
        .find(item => item.dataset.transcriptKey === anchor.key)
      if (element && row) {
        const delta = row.getBoundingClientRect().top - element.getBoundingClientRect().top + anchor.offset
        if (Math.abs(delta) > 1) virtuoso.current?.scrollTo({ top: element.scrollTop + delta, behavior: 'auto' })
      } else {
        virtuoso.current?.scrollToIndex({ index, align: 'start', offset: anchor.offset, behavior: 'auto' })
      }
    })
  }, [controller, virtuoso])
  const remember = useCallback(() => {
    if (!scroller?.isConnected) return
    const top = scroller.getBoundingClientRect().top
    const row = Array.from(scroller.querySelectorAll<HTMLElement>('[data-transcript-key]'))
      .find(item => item.getBoundingClientRect().bottom > top)
    if (row) saved.anchor = { key: row.dataset.transcriptKey!, offset: top - row.getBoundingClientRect().top }
  }, [saved, scroller])
  const preserveReadingPosition = useCallback(() => {
    pause()
    remember()
    focusAnchor.current = saved.anchor
  }, [pause, remember, saved])
  const preserveDisclosure = useCallback((event: React.MouseEvent) => {
    const target = event.target instanceof Element ? event.target : null
    if (!target?.closest('button,summary')) return
    const row = target.closest<HTMLElement>('[data-transcript-key]')
    if (!row || !scroller) return
    pause()
    const anchor = { key: row.dataset.transcriptKey!, offset: scroller.getBoundingClientRect().top - row.getBoundingClientRect().top }
    focusAnchor.current = anchor
    saved.anchor = anchor
  }, [pause, saved, scroller])

  // Callback refs attach on late hydration as well as the initial render.
  const scrollerRef = useCallback((node: HTMLElement | Window | null) => {
    scrollerElement.current = node instanceof HTMLElement ? node : null
    setScroller(scrollerElement.current)
  }, [])
  useEffect(() => {
    if (!scroller) return
    mounted.current = true
    let lastTop = scroller.scrollTop
    let manual = false
    let touchY = 0
    // A downward gesture at the end moves nothing. Counting it as manual left
    // a stale flag that later read a layout shrink (e.g. a sent message being
    // replaced) as the user scrolling up, and the transcript stopped following.
    const atEnd = () => scroller.scrollHeight - scroller.scrollTop - scroller.clientHeight <= 1
    const wheel = (event: WheelEvent) => {
      if (event.ctrlKey || nestedScroller(event.target, scroller, event.deltaY)) return
      if (event.deltaY >= 0 && atEnd()) return
      manual = true
      if (event.deltaY < 0) pause()
    }
    const pointer = (event: PointerEvent) => {
      // Scrollbar drags target the scroller itself. Content controls are handled
      // separately and must remain clickable; do not prevent any native event.
      if (event.target === scroller) { manual = true; pause() }
    }
    const key = (event: KeyboardEvent) => {
      const target = event.target instanceof Element ? event.target : null
      if (target?.closest('input,textarea,select,[contenteditable="true"]')) return
      if (['ArrowUp', 'PageUp', 'Home'].includes(event.key) || (event.key === ' ' && event.shiftKey)) {
        manual = true; pause()
      } else if (['ArrowDown', 'PageDown', 'End', ' '].includes(event.key) && !atEnd()) manual = true
    }
    const touchStart = (event: TouchEvent) => { touchY = event.touches[0]?.clientY ?? 0 }
    const touchMove = (event: TouchEvent) => {
      const y = event.touches[0]?.clientY ?? touchY
      const delta = touchY - y
      touchY = y
      if (nestedScroller(event.target, scroller, delta)) return
      if (delta >= 0 && atEnd()) return
      manual = true
      if (delta < 0) pause()
    }
    const scroll = () => {
      const top = scroller.scrollTop
      if (manual && top < lastTop - 1) pause()
      if (manual && top > lastTop && scroller.scrollHeight - top - scroller.clientHeight < 24) {
        manual = false
        jumpToLatest()
      }
      lastTop = top
      remember()
    }
    scroller.addEventListener('wheel', wheel, { passive: true })
    scroller.addEventListener('pointerdown', pointer)
    scroller.addEventListener('keydown', key)
    scroller.addEventListener('touchstart', touchStart, { passive: true })
    scroller.addEventListener('touchmove', touchMove, { passive: true })
    scroller.addEventListener('scroll', scroll, { passive: true })
    // The chat area changes height when the message box grows or shrinks while typing. Correct the
    // position right here, before the browser paints: waiting for the next animation frame (as
    // layoutChanged does) drew one frame at the old offset, a small visible jump per line typed.
    let lastHeight = scroller.clientHeight
    const observer = new ResizeObserver(() => {
      const height = scroller.clientHeight
      if (height !== lastHeight) {
        const grewBy = lastHeight - height
        lastHeight = height
        if (controller.following) {
          scroller.scrollTop = Math.max(0, scroller.scrollHeight - height)
        } else if (grewBy !== 0) {
          // Reading back in the transcript: keep the text under the reader's eye in place.
          scroller.scrollTop = Math.max(0, scroller.scrollTop + grewBy)
        }
      }
      layoutChanged()
    })
    observer.observe(scroller)
    if (saved.following) layoutChanged()
    return () => {
      remember()
      mounted.current = false
      if (blankCheck.current !== null) window.clearTimeout(blankCheck.current)
      blankCheck.current = null
      observer.disconnect()
      controller.cancelPending()
      cancelRestore()
      scroller.removeEventListener('wheel', wheel)
      scroller.removeEventListener('pointerdown', pointer)
      scroller.removeEventListener('keydown', key)
      scroller.removeEventListener('touchstart', touchStart)
      scroller.removeEventListener('touchmove', touchMove)
      scroller.removeEventListener('scroll', scroll)
    }
  }, [scroller, controller, saved, pause, jumpToLatest, layoutChanged, remember, cancelRestore])
  useEffect(() => { if (mounted.current) layoutChanged() }, [keys, layoutChanged])
  useEffect(() => {
    if (!readingKey) return
    const follow = (event: Event) => {
      if ((event as CustomEvent<string>).detail === readingKey) jumpToLatest()
    }
    window.addEventListener(FOLLOW_LATEST_EVENT, follow)
    return () => window.removeEventListener(FOLLOW_LATEST_EVENT, follow)
  }, [readingKey, jumpToLatest])
  return { following, scrollerRef, layoutChanged, jumpToLatest, pause, preserveDisclosure, preserveReadingPosition }
}
