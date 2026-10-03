import { useEffect, useRef } from 'react'
import type { ClipboardEvent } from 'react'
import { useChatStore } from '../../stores/useChatStore'

const MAX_CLIPBOARD_TEXT = 64 * 1024
// Even control characters escaped by JSON stay below the viewer's 16 KiB limit.
export function browserTextChunks(text: string): string[] {
  const characters = Array.from(text)
  const chunks: string[] = []
  for (let i = 0; i < characters.length; i += 1024) chunks.push(characters.slice(i, i + 1024).join(''))
  return chunks
}

export function useBrowserClipboard(session: string, controlling: boolean, send: (message: Record<string, unknown>) => void) {
  const active = useRef(controlling)
  active.current = controlling
  const sequence = useRef(0)
  const pending = useRef(new Map<string, { resolve: (text: string) => void; reject: (error: Error) => void; timeout: ReturnType<typeof setTimeout> }>())
  const fail = () => useChatStore.getState().addToast('Unable to use the clipboard. Check browser clipboard access and try again.', 'error')
  useEffect(() => () => {
    for (const request of pending.current.values()) { clearTimeout(request.timeout); request.reject(new Error('Browser control ended')) }
    pending.current.clear()
  }, [session, controlling])

  function receive(message: { requestId?: string; text?: string; error?: string }) {
    const request = pending.current.get(message.requestId || '')
    if (!request) return
    clearTimeout(request.timeout)
    pending.current.delete(message.requestId!)
    if (!active.current || message.error || typeof message.text !== 'string') request.reject(new Error(message.error || 'Unable to copy'))
    else request.resolve(message.text)
  }

  async function copy() {
    if (!active.current) return
    if (!navigator.clipboard?.writeText) { fail(); return }
    const requestId = `copy-${++sequence.current}`
    const text = new Promise<string>((resolve, reject) => {
      const timeout = setTimeout(() => { pending.current.delete(requestId); reject(new Error('Clipboard request timed out')) }, 10000)
      pending.current.set(requestId, { resolve, reject, timeout })
      send({ type: 'clipboard_copy', requestId })
    })
    // Clipboard API failures can precede the asynchronous server reply.
    void text.catch(() => {})
    try {
      // Promise-backed ClipboardItem preserves the original user gesture on
      // browsers that require clipboard access before the server reply arrives.
      if (navigator.clipboard.write && typeof ClipboardItem !== 'undefined') {
        await navigator.clipboard.write([new ClipboardItem({ 'text/plain': text.then(value => new Blob([value], { type: 'text/plain' })) })])
      } else {
        const value = await text
        if (active.current) await navigator.clipboard.writeText(value)
      }
    } catch (error) {
      if (error instanceof Error && error.message === 'Select some text before copying.') useChatStore.getState().addToast(error.message, 'info')
      else fail()
    }
  }

  function paste(event: ClipboardEvent<HTMLTextAreaElement>) {
    event.preventDefault()
    pasteText(event.clipboardData.getData('text/plain'))
  }
  function pasteText(text: string) {
    if (!active.current) return
    if (text.length > MAX_CLIPBOARD_TEXT) {
      useChatStore.getState().addToast('Copy a smaller selection of text and try again.', 'error')
      return
    }
    for (const chunk of browserTextChunks(text)) send({ type: 'input_text', text: chunk })
  }
  async function pasteFromClipboard() {
    if (!active.current) return
    try { pasteText(await navigator.clipboard.readText()) } catch { fail() }
  }
  return { copy, paste, pasteFromClipboard, receive }
}
