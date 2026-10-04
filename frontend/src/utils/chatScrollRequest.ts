import { useChatStore } from '../stores/useChatStore'

export const CHAT_SCROLL_TO_BOTTOM_EVENT = 'chat-scroll-to-bottom'

/**
 * The person just switched to a chat, or a message was just sent into it: show
 * the bottom. Dispatch exactly once; ChatArea coalesces requests and performs
 * the scroll after the transcript has settled, so callers must not repeat it
 * on timers.
 */
export function requestChatScrollToBottom(): void {
  useChatStore.getState().setAutoScroll(true)
  window.dispatchEvent(new CustomEvent(CHAT_SCROLL_TO_BOTTOM_EVENT))
}

/**
 * One deferred "go to the bottom": requests made while it is pending coalesce
 * into one scroll, performed once the list has been quiet for `quietMs` (callers
 * report activity through `touch`) or `maxMs` after the first request, so a
 * list that never settles cannot hold it back forever. The newest request's
 * `stillValid` decides whether to scroll (manual scrolling since then wins).
 */
export class SettledScroll {
  private quiet: ReturnType<typeof setTimeout> | null = null
  private cap: ReturnType<typeof setTimeout> | null = null
  private valid: (() => boolean) | null = null
  private perform: () => void
  private quietMs: number
  private maxMs: number
  constructor(perform: () => void, quietMs = 120, maxMs = 800) {
    this.perform = perform
    this.quietMs = quietMs
    this.maxMs = maxMs
  }

  get pending(): boolean { return this.valid !== null }

  request(stillValid: () => boolean = () => true): void {
    this.valid = stillValid
    this.cap ??= setTimeout(() => this.fire(), this.maxMs)
    this.touch()
  }

  /** The list changed (rows mounted, resized): wait for it to go quiet again. */
  touch(): void {
    if (!this.valid) return
    if (this.quiet) clearTimeout(this.quiet)
    this.quiet = setTimeout(() => this.fire(), this.quietMs)
  }

  cancel(): void {
    if (this.quiet) clearTimeout(this.quiet)
    if (this.cap) clearTimeout(this.cap)
    this.quiet = this.cap = this.valid = null
  }

  private fire(): void {
    const valid = this.valid
    this.cancel()
    if (valid?.()) this.perform()
  }
}
