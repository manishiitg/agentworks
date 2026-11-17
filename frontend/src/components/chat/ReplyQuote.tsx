import { useEffect, useRef, useState } from 'react'
import { createPortal } from 'react-dom'
import { Quote } from 'lucide-react'
import { useChatStore } from '../../stores/useChatStore'
import { CHAT_FOCUS_COMPOSER_EVENT } from '../../utils/workspacePaneChat'

const MAX_QUOTE = 2000
/** Asks the tab's mounted composer to add a quote to what is typed now. */
export const CHAT_INSERT_QUOTE_EVENT = 'chat-insert-quote'
export type InsertQuoteDetail = { tabId: string; text: string; handled?: boolean }

/** The selected reply text as a Markdown quote block. */
export function quoteBlock(text: string): string {
  let body = text.replace(/\r\n/g, '\n').replace(/\n{3,}/g, '\n\n').trim()
  if (body.length > MAX_QUOTE) body = `${body.slice(0, MAX_QUOTE - 1).trimEnd()}…`
  return body.split('\n').map(line => line.trim() ? `> ${line}` : '>').join('\n')
}

/** Adds a quote below any draft, leaving the caret on a new line after it. */
export function withQuote(draft: string, text: string): string {
  const existing = draft.trimEnd()
  return `${existing ? `${existing}\n\n` : ''}${quoteBlock(text)}\n\n`
}

/** Selecting text in one of this chat's replies shows a Quote button; it puts
 *  the text in the message box as a quote. Front-end only: the agent gets the
 *  quote as part of the message. Mounted beside the chat's transcript. */
export function ReplyQuote({ tabId, disabled }: { tabId: string; disabled?: boolean }) {
  const marker = useRef<HTMLSpanElement>(null)
  const [shown, setShown] = useState<{ text: string; top: number; left: number } | null>(null)

  useEffect(() => {
    if (disabled) { setShown(null); return }
    let frame = 0
    const read = () => {
      cancelAnimationFrame(frame)
      frame = requestAnimationFrame(() => {
        const selection = window.getSelection()
        const scope = marker.current?.parentElement
        if (!selection || selection.isCollapsed || selection.rangeCount === 0 || !scope) { setShown(null); return }
        const reply = (node: Node | null) => (node instanceof Element ? node : node?.parentElement)?.closest('[data-theme-message-role="assistant"]')
        const start = reply(selection.anchorNode), end = reply(selection.focusNode)
        const text = selection.toString()
        if (!start || start !== end || !scope.contains(start) || !text.trim()) { setShown(null); return }
        const rect = selection.getRangeAt(0).getBoundingClientRect()
        if (!rect.width && !rect.height) { setShown(null); return }
        setShown({ text, top: Math.max(8, rect.top - 34), left: Math.min(window.innerWidth - 90, Math.max(8, rect.left + rect.width / 2 - 36)) })
      })
    }
    const hide = () => setShown(null)
    document.addEventListener('selectionchange', read)
    window.addEventListener('resize', hide)
    document.addEventListener('scroll', hide, true)
    return () => {
      cancelAnimationFrame(frame)
      document.removeEventListener('selectionchange', read)
      window.removeEventListener('resize', hide)
      document.removeEventListener('scroll', hide, true)
    }
  }, [disabled])

  const quote = () => {
    if (!shown) return
    // The composer holds keystrokes not yet saved to the store, so it adds
    // the quote itself; without a mounted composer the saved draft gets it.
    const detail: InsertQuoteDetail = { tabId, text: shown.text }
    window.dispatchEvent(new CustomEvent<InsertQuoteDetail>(CHAT_INSERT_QUOTE_EVENT, { detail }))
    if (!detail.handled) {
      const store = useChatStore.getState()
      store.setTabConfig(tabId, { inputText: withQuote(store.getTabConfig(tabId)?.inputText || '', shown.text) })
      window.setTimeout(() => window.dispatchEvent(new CustomEvent(CHAT_FOCUS_COMPOSER_EVENT)), 30)
    }
    window.getSelection()?.removeAllRanges()
    setShown(null)
  }

  return <>
    <span ref={marker} hidden />
    {shown && createPortal(<button type="button" aria-label="Quote in message"
      className="fixed z-50 inline-flex items-center gap-1 rounded-md border border-border bg-popover px-2 py-1 text-xs text-popover-foreground shadow-md hover:bg-muted"
      style={{ top: shown.top, left: shown.left }}
      onMouseDown={event => event.preventDefault()} onClick={quote}>
      <Quote className="h-3 w-3" aria-hidden="true" />Quote
    </button>, document.body)}
  </>
}
