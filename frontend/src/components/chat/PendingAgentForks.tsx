import { actOnAgentFork } from './agentForkApi'
import { useCallback, useEffect, useState } from 'react'
import { createPortal } from 'react-dom'
import { teamError, teamRequest, type TeamTabState } from '../../products/work/teamApi'

type ForkOffer = { id: string; sender_id: string; recipient_id: string; ready: boolean; agent: { name: string } }

/** No surface-owned transfer state: every notice reads the pending server record. */
export function PendingAgentForks({ state }: { state: TeamTabState }) {
  const [offers, setOffers] = useState<ForkOffer[]>([])
  const [open, setOpen] = useState(false)
  const [busy, setBusy] = useState('')
  const [error, setError] = useState('')
  const target = state.target
  const me = state.view?.people.find(person => person.you)?.user_id
  const reload = useCallback(async () => {
    if (!target || !state.view?.team.on || !me) { setOffers([]); return }
    try {
      const data = await teamRequest<{ forks: ForkOffer[] }>(target, '/agent-forks')
      setOffers(data.forks.filter(offer => offer.ready && offer.recipient_id === me))
    } catch { setOffers([]) }
  }, [target?.project, target?.connection, state.view, me])
  // Team's existing crew_panel notice refreshes state.view. No new polling.
  useEffect(() => { void reload() }, [reload])
  async function act(offer: ForkOffer, operation: 'accept' | 'cancel') {
    if (!target || busy) return
    setBusy(offer.id); setError('')
    try {
      const result = await actOnAgentFork<{ accepted?: boolean }>(target, operation, { fork_id: offer.id })
      if (operation === 'accept' && !result.accepted) throw new Error('Finish downloading this copy on your computer before accepting it.')
      await reload()
    } catch (error) { setError(teamError(error)) }
    finally { setBusy('') }
  }
  if (!offers.length) return null
  return <>
    <button type="button" className="shrink-0 rounded-md px-2 py-1 text-xs text-primary hover:bg-primary/10" onClick={() => setOpen(true)}>{offers.length === 1 ? 'Agent copy ready' : `${offers.length} agent copies ready`}</button>
    {open && createPortal(<div className="fixed inset-0 z-[100] flex items-center justify-center bg-black/30 p-4" onClick={() => { if (!busy) setOpen(false) }}>
      <section role="dialog" aria-modal="true" aria-labelledby="pending-forks-title" className="w-full max-w-md space-y-4 rounded-xl border border-border bg-background p-5 shadow-xl" onClick={event => event.stopPropagation()}>
        <h2 id="pending-forks-title" className="font-semibold">Agent copies ready for you</h2>
        {offers.map(offer => <div key={offer.id} className="space-y-2">
          <p className="text-sm">{offer.sender_id === me ? `Your copy of ${offer.agent.name} is ready.` : `${state.view?.people.find(person => person.user_id === offer.sender_id)?.name || 'A teammate'} shared a copy of ${offer.agent.name} with you.`}</p>
          <p className="text-xs text-muted-foreground">The whole conversation is included. Your new agent starts Not shared, using your own login. Their agent stays unchanged.</p>
          <div className="flex justify-end gap-2"><button type="button" className="rounded-md px-3 py-2 text-sm hover:bg-muted" disabled={!!busy} onClick={() => { void act(offer, 'cancel') }}>Decline</button><button type="button" className="rounded-md bg-primary px-3 py-2 text-sm text-primary-foreground disabled:opacity-50" disabled={!!busy} onClick={() => { void act(offer, 'accept') }}>{busy === offer.id ? 'Please wait…' : 'Accept copy'}</button></div>
        </div>)}
        {error && <p role="alert" className="text-sm text-destructive">{error}</p>}
        <button type="button" disabled={!!busy} className="rounded-md px-3 py-2 text-sm hover:bg-muted" onClick={() => setOpen(false)}>Close</button>
      </section>
    </div>, document.body)}
  </>
}
