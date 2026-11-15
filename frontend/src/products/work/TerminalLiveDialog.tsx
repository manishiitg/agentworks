import { useEffect, useState } from 'react'
import { X } from 'lucide-react'
import ModalPortal from '../../components/ui/ModalPortal'
import { teamRequest, teamFailure, type TeamTarget } from './teamApi'

/** Native text is fetched only by the server's opt-in Live route, never the preview feed. */
export function TerminalLiveDialog({ target, agent, close }: { target: TeamTarget; agent: { id: string; name: string }; close: () => void }) {
  type Message = { Role: string; Parts: { Text: string }[] }
  const [messages, setMessages] = useState<Message[]>([])
  const [error, setError] = useState('')
  const [loaded, setLoaded] = useState(false)
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
      <header className="mb-3 flex items-center justify-between"><h2 className="font-semibold">{agent.name} · Live conversation</h2><button type="button" aria-label="Close" onClick={close}><X className="h-4 w-4" /></button></header>
      {error ? <p role="alert" className="text-muted-foreground">{error}</p> : !loaded ? <p role="status">Loading…</p> : messages.length === 0 ? <p className="text-muted-foreground">No messages yet.</p> : <div className="space-y-4">{messages.map((message, index) => <article key={index}><p className="mb-1 text-xs text-muted-foreground">{message.Role === 'user' ? 'Person' : 'Agent'}</p><p className="whitespace-pre-wrap break-words">{message.Parts.map(part => part.Text).join('\n\n')}</p></article>)}</div>}
    </section>
  </div></ModalPortal>
}
