import { useEffect, useState } from 'react'
import { X } from 'lucide-react'
import ModalPortal from '../../components/ui/ModalPortal'
import { teamRequest, teamFailure, type TeamTarget } from './teamApi'

/** Native text is fetched only by the server's opt-in Live route, never the preview feed. */
export function TerminalLiveDialog({ target, agent, close, owner = false }: { target: TeamTarget; agent: { id: string; name: string }; close: () => void; owner?: boolean }) {
  type Message = { Role: string; Parts: { Text: string }[] }
  const [messages, setMessages] = useState<Message[]>([])
  const [error, setError] = useState('')
  const [loaded, setLoaded] = useState(false)
  const [draft, setDraft] = useState('')
  const [sending, setSending] = useState(false)
  const [mailError, setMailError] = useState('')
  const [inbox, setInbox] = useState('')
  const [replies, setReplies] = useState<{ message_id: string; message: string }[]>([])
  useEffect(() => { setInbox(''); setReplies([]); setDraft(''); setMailError('') }, [target.project, target.connection, agent.id])
  useEffect(() => {
    if (!owner || !inbox) return
    let active = true
    let timer: ReturnType<typeof setTimeout> | undefined
    const read = async () => {
      try {
        if (document.visibilityState === 'visible') {
          const data = await teamRequest<{ messages: { message_id: string; message: string }[] }>(target, `/terminal/${encodeURIComponent(agent.id)}/ask`, 'POST', { inbox_id: inbox })
          if (active) { setReplies(data.messages); setMailError('') }
        }
      } catch (e) { if (active) { setReplies([]); setMailError(teamFailure(e).message) } }
      finally { if (active) timer = setTimeout(() => void read(), 5000) }
    }
    void read()
    return () => { active = false; clearTimeout(timer) }
  }, [owner, inbox, target.project, target.connection, agent.id]) // eslint-disable-line react-hooks/exhaustive-deps
  const ask = async () => {
    if (!draft.trim() || sending) return
    setSending(true); setMailError('')
    try {
      const data = await teamRequest<{ inbox_id: string }>(target, `/terminal/${encodeURIComponent(agent.id)}/ask`, 'POST', { message: draft, inbox_id: inbox || undefined, submission_id: crypto.randomUUID() })
      setInbox(data.inbox_id); setDraft('')
    } catch (e) { setMailError(teamFailure(e).message) }
    finally { setSending(false) }
  }
  useEffect(() => {
    let active = true
    let timer: ReturnType<typeof setTimeout> | undefined
    setMessages([]); setError(''); setLoaded(false)
    const load = async () => {
      if (document.visibilityState !== 'visible') {
        if (active) timer = setTimeout(() => void load(), 5000)
        return
      }
      try {
        const data = await teamRequest<{ messages: Message[] }>(target, `/terminal/${encodeURIComponent(agent.id)}/live`)
        if (active) { setMessages(data.messages); setError(''); setLoaded(true) }
      } catch (e) {
        if (active) { setMessages([]); setError(teamFailure(e).message); setLoaded(true) }
      } finally {
        if (active) timer = setTimeout(() => void load(), 5000)
      }
    }
    void load()
    return () => { active = false; clearTimeout(timer) }
  }, [target.project, target.connection, agent.id]) // eslint-disable-line react-hooks/exhaustive-deps
  return <ModalPortal><div className="fixed inset-0 z-50 flex items-center justify-center bg-black/40 p-4" onClick={close}>
    <section role="dialog" aria-modal="true" aria-label={agent.name} className="max-h-[80vh] w-full max-w-2xl overflow-y-auto rounded-xl border border-border bg-background p-4 text-sm shadow-xl" onClick={event => event.stopPropagation()}>
      <header className="mb-3 flex items-center justify-between"><h2 className="font-semibold">{agent.name} · {owner ? 'Your terminal conversation' : 'Live conversation'}</h2><button type="button" aria-label="Close" onClick={close}><X className="h-4 w-4" /></button></header>
      {owner && <p className="mb-4 text-xs text-muted-foreground">Your exact native session. Teammates still get Answers only unless you choose Live. The ask box uses AgentBridge, never terminal typing.</p>}
      {error ? <p role="alert" className="text-muted-foreground">{error}</p> : !loaded ? <p role="status">Loading…</p> : messages.length === 0 ? <p className="text-muted-foreground">No messages yet.</p> : <div className="space-y-4">{messages.map((message, index) => <article key={index}><p className="mb-1 text-xs text-muted-foreground">{message.Role === 'user' ? 'Person' : 'Agent'}</p><p className="whitespace-pre-wrap break-words">{message.Parts.map(part => part.Text).join('\n\n')}</p></article>)}</div>}
      {owner && <section aria-label="Ask this terminal agent" className="mt-5 border-t border-border pt-4">
        {replies.map(reply => <p key={reply.message_id} className="mb-3 whitespace-pre-wrap break-words text-sm">{reply.message}</p>)}
        <form onSubmit={event => { event.preventDefault(); void ask() }} className="rounded-xl border border-border p-3">
          <textarea aria-label={`Ask ${agent.name}`} placeholder={`Ask ${agent.name}…`} value={draft} onChange={event => setDraft(event.target.value)} disabled={sending} className="min-h-16 w-full resize-y bg-transparent text-sm outline-none" />
          <div className="flex items-center justify-between gap-3"><span className="text-xs text-muted-foreground">AgentBridge · direct mail waits if the terminal is closed</span><button type="submit" disabled={sending || !draft.trim()} className="rounded-full bg-foreground px-3 py-1.5 text-xs text-background disabled:opacity-50">{sending ? 'Sending…' : 'Ask agent'}</button></div>
        </form>
        {inbox && <p role="status" className="mt-2 text-xs text-muted-foreground">Sent over AgentBridge. Answers return here in the same inbox.</p>}
        {mailError && <p role="alert" className="mt-2 text-xs text-destructive">{mailError}</p>}
      </section>}
    </section>
  </div></ModalPortal>
}
