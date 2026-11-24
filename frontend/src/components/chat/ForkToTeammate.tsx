import { actOnAgentFork } from './agentForkApi'
import { useEffect, useState } from 'react'
import { createPortal } from 'react-dom'
import { teamError, type TeamTabState } from '../../products/work/teamApi'

/** One maker confirmation sends the whole conversation, never changes its owner. */
export function ForkToTeammate({ state, agentId, name, close }: { state: TeamTabState; agentId: string; name: string; close: () => void }) {
  const people = state.view?.people.filter(person => !person.you && person.note !== 'invited') || []
  const [recipient, setRecipient] = useState('')
  const [busy, setBusy] = useState(false)
  const [sent, setSent] = useState(false)
  const [error, setError] = useState('')
  useEffect(() => {
    const escape = (event: KeyboardEvent) => { if (event.key === 'Escape' && !busy) { event.stopPropagation(); close() } }
    document.addEventListener('keydown', escape)
    return () => document.removeEventListener('keydown', escape)
  }, [busy, close])
  const person = people.find(item => item.user_id === recipient)
  async function send() {
    if (!state.target || !person || busy) return
    setBusy(true); setError('')
    try {
      await actOnAgentFork(state.target, 'send', { agent_id: agentId, recipient_id: person.user_id })
      setSent(true)
    } catch (error) { setError(teamError(error)) }
    finally { setBusy(false) }
  }
  return createPortal(<div className="fixed inset-0 z-[100] flex items-center justify-center bg-black/30 p-4" onClick={() => { if (!busy) close() }}>
    <section role="dialog" aria-modal="true" aria-labelledby="fork-to-title" className="w-full max-w-md max-h-[calc(100dvh-2rem)] overflow-y-auto space-y-4 rounded-xl border border-border bg-background p-5 shadow-xl" onClick={event => event.stopPropagation()}>
      <h2 id="fork-to-title" className="font-semibold">{sent ? 'Copy sent' : `Send a copy of ${name}`}</h2>
      {sent ? <p className="text-sm">{person?.name} can accept their own private copy. Your agent keeps running unchanged.</p> : <>
        <label className="block space-y-1 text-sm"><span>Teammate in this project</span><select aria-label="Teammate in this project" className="w-full rounded-md border border-border bg-background p-2" value={recipient} onChange={event => setRecipient(event.target.value)} disabled={busy}>
          <option value="">Choose a teammate</option>
          {people.map(person => <option key={person.user_id} value={person.user_id}>{person.name}</option>)}
        </select></label>
        {person && <p className="text-sm">{person.name} will get a copy of this whole conversation, including anything you pasted into it.</p>}
        <p className="text-xs text-muted-foreground">Their copy starts Not shared. Files, memory, skills and logins are not included. Unaccepted copies expire after 7 days.</p>
        {error && <p role="alert" className="text-sm text-destructive">{error}</p>}
      </>}
      <div className="flex justify-end gap-2"><button type="button" disabled={busy} className="rounded-md px-3 py-2 text-sm hover:bg-muted" onClick={close}>{sent ? 'Done' : 'Cancel'}</button>
        {!sent && <button type="button" disabled={!person || busy} className="rounded-md bg-primary px-3 py-2 text-sm text-primary-foreground disabled:opacity-50" onClick={() => { void send() }}>{busy ? 'Sending copy…' : 'Send copy'}</button>}
      </div>
    </section>
  </div>, document.body)
}
