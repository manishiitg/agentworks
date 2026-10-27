import { useCallback, useEffect, useMemo, useRef, useState, type RefObject } from 'react'
import { ChevronDown, ChevronUp, X } from 'lucide-react'
import { currentChatFindRoot, findTranscriptMatches, OPEN_CHAT_FIND, type TranscriptSearchRow } from '../../utils/transcriptSearch'

export function TranscriptFind({ rootRef, rows, onSelect, hasOlder, loadingOlder, onLoadOlder }: {
  rootRef: RefObject<HTMLDivElement | null>
  rows: () => TranscriptSearchRow[]
  onSelect: (key: string | null, query: string) => void
  hasOlder: boolean
  loadingOlder: boolean
  onLoadOlder?: () => void
}) {
  const [open, setOpen] = useState(false)
  const [query, setQuery] = useState('')
  const [selectedKey, setSelectedKey] = useState<string | null>(null)
  const input = useRef<HTMLInputElement>(null)
  const previousFocus = useRef<HTMLElement | null>(null)
  const matches = useMemo(() => open && query.trim() ? findTranscriptMatches(rows(), query) : [], [open, rows, query])
  const index = Math.max(0, matches.findIndex(match => match.key === selectedKey))
  const selected = matches[index]
  const close = useCallback(() => {
    setOpen(false)
    onSelect(null, '')
    const target = previousFocus.current?.isConnected ? previousFocus.current : rootRef.current
    target?.focus({ preventScroll: true })
  }, [onSelect, rootRef])
  useEffect(() => {
    const root = rootRef.current
    if (!root) return
    const show = () => {
      if (!open) previousFocus.current = document.activeElement instanceof HTMLElement && document.activeElement !== document.body ? document.activeElement : null
      setOpen(true)
      input.current?.focus()
      input.current?.select()
    }
    const keyboard = (event: KeyboardEvent) => {
      if (event.defaultPrevented || event.isComposing || event.altKey || event.shiftKey || !(event.ctrlKey || event.metaKey) || event.key.toLowerCase() !== 'f') return
      const target = event.target instanceof HTMLElement ? event.target : document.activeElement
      if (target?.closest('[role="dialog"],.monaco-editor,.cm-editor')) return
      const scope = root.closest('[data-chat-find-scope]') || root
      if (!target || target === document.body || target === document.documentElement) {
        if (currentChatFindRoot() !== root) return
      } else {
        if (!scope.contains(target) || (scope !== root && target.closest('[data-chat-find-scope]') !== scope)) return
        const editor = target.closest('input,textarea,select,[contenteditable="true"]')
        if (editor && editor !== input.current && !editor.hasAttribute('data-chat-composer')) return
      }
      event.preventDefault()
      show()
    }
    root.addEventListener(OPEN_CHAT_FIND, show)
    window.addEventListener('keydown', keyboard)
    return () => { root.removeEventListener(OPEN_CHAT_FIND, show); window.removeEventListener('keydown', keyboard) }
  }, [open, rootRef])
  useEffect(() => { if (open) input.current?.focus() }, [open])
  useEffect(() => {
    if (open) {
      setSelectedKey(selected?.key ?? null)
      onSelect(selected?.key ?? null, query)
    }
  }, [open, selected?.key, query, onSelect])
  const move = (delta: number) => {
    if (matches.length) setSelectedKey(matches[(index + delta + matches.length) % matches.length].key)
  }
  if (!open) return null
  return <div role="search" aria-label="Find in this chat" onKeyDown={event => {
    if (event.key === 'Escape' && !event.nativeEvent.isComposing) { event.preventDefault(); event.stopPropagation(); close() }
  }} className="shrink-0 border-b border-border bg-background px-3 py-2 text-xs">
    <div className="flex items-center gap-2">
      <input ref={input} aria-label="Find in loaded messages" placeholder="Find in loaded messages" value={query}
        className="min-w-0 flex-1 rounded border border-border bg-transparent px-2 py-1 text-sm outline-none focus:ring-1 focus:ring-primary"
        onChange={event => { setQuery(event.target.value); setSelectedKey(null) }}
        onKeyDown={event => {
          if (event.nativeEvent.isComposing) return
          if (event.key === 'Enter') { event.preventDefault(); move(event.shiftKey ? -1 : 1) }
        }} />
      <span aria-live="polite" className="shrink-0 text-muted-foreground">{query.trim() ? matches.length ? `${index + 1} of ${matches.length}` : 'No matches' : 'Loaded messages'}</span>
      <button type="button" aria-label="Previous match" disabled={!matches.length} onClick={() => move(-1)} className="rounded p-1 disabled:opacity-40"><ChevronUp size={16} /></button>
      <button type="button" aria-label="Next match" disabled={!matches.length} onClick={() => move(1)} className="rounded p-1 disabled:opacity-40"><ChevronDown size={16} /></button>
      <button type="button" aria-label="Close chat search" onClick={close} className="rounded p-1"><X size={16} /></button>
    </div>
    {selected && <div className="mt-1 truncate text-muted-foreground">{selected.before}<mark className="bg-yellow-200 text-black">{selected.match}</mark>{selected.after}</div>}
    {hasOlder && <div className="mt-1 text-muted-foreground">Search covers loaded messages and steps. <button type="button" className="underline disabled:opacity-40" disabled={loadingOlder || !onLoadOlder} onClick={onLoadOlder}>{loadingOlder ? 'Loading older messages…' : 'Load older messages'}</button></div>}
  </div>
}
