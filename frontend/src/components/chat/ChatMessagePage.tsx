import { useEffect, useRef, useState } from 'react'
import api from '../../services/api'
import { MarkdownRenderer } from '../ui/MarkdownRenderer'

interface Message { session_id: string; id: string; sequence: number; at: string; role: string; text: string }
interface MessagePage { title: string; messages: Message[]; selected_id: string }

/** A saved-message link is a reference, never a writable binding to a chat. */
export function ChatMessagePage() {
  const [page, setPage] = useState<MessagePage>()
  const [error, setError] = useState('')
  const selected = useRef<HTMLElement>(null)
  useEffect(() => {
    const controller = new AbortController()
    const params = new URLSearchParams(window.location.search)
    void api.post<MessagePage>('/api/chat-search/message', { chat: params.get('chat'), id: params.get('id'), sequence: Number(params.get('sequence')) }, { signal: controller.signal, skipSessionContext: true })
      .then(({ data }) => { if (!controller.signal.aborted) setPage(data) })
      .catch(() => { if (!controller.signal.aborted) setError('This message is unavailable. Return to your workspace, or ask the chat owner for access.') })
    return () => controller.abort()
  }, [])
  useEffect(() => { selected.current?.scrollIntoView({ block: 'center' }) }, [page])
  if (error) return <main className="grid h-full place-items-center p-6"><div className="max-w-md space-y-4 text-center text-sm"><p role="alert">{error}</p><a href="/home" className="underline">Your workspace</a></div></main>
  if (!page) return <p role="status" className="p-6 text-sm text-muted-foreground">Opening message…</p>
  const first = page.messages[0], last = page.messages.at(-1)
  const selectedSequence = page.messages.find(message => message.id === page.selected_id)?.sequence || 0
  const href = (message: Message) => `/message?${new URLSearchParams({ chat: message.session_id, id: message.id, sequence: String(message.sequence) })}`
  return <main className="flex h-full min-h-0 flex-col">
    <header className="flex items-center justify-between gap-4 border-b border-border px-4 py-3 text-sm"><div><h1 className="font-medium">{page.title}</h1><p className="text-xs text-muted-foreground">Saved messages · the linked message is highlighted</p></div><a href="/home" className="shrink-0 text-xs underline">Your workspace</a></header>
    <div className="overflow-auto"><div className="mx-auto max-w-3xl space-y-4 px-4 py-6">
      {page.messages.map(message => <article key={message.id} ref={message.id === page.selected_id ? selected : undefined} aria-label={message.id === page.selected_id ? 'Linked message' : undefined} className={`rounded-xl border p-4 ${message.id === page.selected_id ? 'border-primary bg-primary/5' : 'border-border'}`}>
        <div className="mb-3 flex items-center justify-between gap-4 text-xs text-muted-foreground"><span>{message.role === 'user' ? 'Message' : 'Agent'} · {new Date(message.at).toLocaleString()}</span><a href={href(message)} className="underline">Link to message</a></div>
        <MarkdownRenderer content={message.text} untrustedContent disablePathLinking />
      </article>)}
      <nav className="flex justify-between text-xs underline" aria-label="Nearby messages">{first && first.sequence < selectedSequence && <a href={href(first)}>Earlier messages</a>}{last && last.sequence > selectedSequence && <a href={href(last)}>Later messages</a>}</nav>
    </div></div>
  </main>
}
