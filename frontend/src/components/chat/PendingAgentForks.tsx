import { actOnAgentFork } from './agentForkApi'
import { useCallback, useEffect, useState } from 'react'
import { createPortal } from 'react-dom'
import { teamError, teamRequest, type TeamTabState } from '../../products/work/teamApi'
import { useChatStore } from '../../stores/useChatStore'
import { GitFork } from 'lucide-react'
import { WorkspaceContextRow } from '../workspace/WorkspaceContextCard'

type ForkOffer = { id: string; sender_id: string; recipient_id: string; ready: boolean; self_fork?: boolean; agent: { name: string } }
type ForkJob = { id: string; fork_id?: string; name: string; stage: string; line: string; percent: number; error?: string }

// Shown in the header and the Team menu at once: a finished accept is announced once, not per place.
const announced = new Set<string>()
const running = (job: ForkJob) => job.stage !== 'done' && job.stage !== 'failed'

/** No surface-owned transfer state: offers are the server's pending records and progress is the Mac's own fork jobs
 * (#3218), so the header and the Team menu show the same thing and a copy keeps going after the dialog closes. */
export function PendingAgentForks({ state, place = 'header' }: { state: TeamTabState; place?: 'header' | 'menu' }) {
  const [offers, setOffers] = useState<ForkOffer[]>([])
  const [jobs, setJobs] = useState<ForkJob[]>([])
  const [open, setOpen] = useState(false)
  const [error, setError] = useState('')
  const target = state.target
  const local = target?.localProject
  const me = state.view?.people?.find(person => person.you)?.user_id
  const reload = useCallback(async () => {
    if (!target || !state.view?.team.on || !me) { setOffers([]); return }
    try {
      const data = await teamRequest<{ forks: ForkOffer[] }>(target, '/agent-forks')
      setOffers((data.forks ?? []).filter(offer => offer.ready && offer.recipient_id === me))
    } catch { setOffers([]) }
  }, [target?.project, target?.connection, state.view, me])
  const readJobs = useCallback(async () => {
    if (!local) { setJobs([]); return [] }
    try {
      const data = await teamRequest<{ jobs: ForkJob[] }>({ project: local }, '/agent-fork-local/jobs')
      setJobs(data.jobs ?? [])
      return data.jobs ?? []
    } catch { return [] }
  }, [local])
  // Team's existing crew_panel notice refreshes state.view. No new polling while nothing is being copied.
  useEffect(() => { void reload(); void readJobs() }, [reload, readJobs])
  // A fork started from a teammate's agent shows its progress here too.
  useEffect(() => {
    const started = () => { void readJobs() }
    window.addEventListener('agentworks:fork-started', started)
    return () => window.removeEventListener('agentworks:fork-started', started)
  }, [readJobs])
  const busy = jobs.some(running)
  useEffect(() => {
    if (!busy) return
    const timer = window.setInterval(() => {
      void readJobs().then(next => {
        let finished = false
        for (const job of next) {
          if (running(job) || !job.fork_id || announced.has(job.id)) continue
          announced.add(job.id); finished = true
          if (job.stage === 'done') useChatStore.getState().addToast(`${job.name || 'The agent'} was copied to your Mac. Open it in your Crew's agent list.`, 'success')
          else useChatStore.getState().addToast(`${job.name || 'The agent'} wasn't copied: ${job.error || 'try again.'}`, 'error')
        }
        if (finished) { void reload(); window.dispatchEvent(new Event('agentworks:team-updated')) }
      })
    }, 1000)
    return () => window.clearInterval(timer)
  }, [busy, readJobs, reload])
  async function accept(offer: ForkOffer) {
    if (!target) return
    setError('')
    try {
      const job = await actOnAgentFork<ForkJob>(target, 'accept', { fork_id: offer.id, name: offer.agent.name }, true)
      setJobs(current => [...current.filter(item => item.id !== job.id), job])
    } catch (cause) { setError(teamError(cause)) }
  }
  async function decline(offer: ForkOffer) {
    if (!target) return
    setError('')
    try { await actOnAgentFork(target, 'cancel', { fork_id: offer.id }); await reload() } catch (cause) { setError(teamError(cause)) }
  }
  const active = jobs.filter(running)
  const jobFor = (offer: ForkOffer) => jobs.filter(job => job.fork_id === offer.id).at(-1)
  if (!offers.length && !active.length) return null
  const percent = active.length ? Math.round(active.reduce((sum, job) => sum + job.percent, 0) / active.length) : 0
  const detail = active.length ? `Copying · ${percent}%` : `${offers.length} ready`
  return <>
    {place === 'menu'
      ? <WorkspaceContextRow icon={GitFork} detail={<span className={active.length ? 'text-primary' : ''}>{detail}</span>} onClick={() => setOpen(true)}>Agent copies</WorkspaceContextRow>
      // Header: the same pill as Team · N working (owner, 20 Nov: the plain link looked out of place).
      : <button type="button" onClick={() => setOpen(true)} aria-label={`Agent copies: ${detail}`} className="flex h-8 shrink-0 items-center gap-1.5 rounded-full border border-border px-2.5 text-xs text-muted-foreground hover:bg-muted">
        <GitFork className={`h-3.5 w-3.5 ${active.length ? 'animate-pulse text-primary' : ''}`} />
        {active.length ? <span className="text-primary">Copying · {percent}%</span> : <span>{offers.length === 1 ? '1 copy ready' : `${offers.length} copies ready`}</span>}
      </button>}
    {open && createPortal(<div className="fixed inset-0 z-[100] flex items-center justify-center bg-background/70 p-4" onClick={() => setOpen(false)}>
      <section role="dialog" aria-modal="true" aria-labelledby="pending-forks-title" className="w-full max-w-md space-y-4 rounded-xl border border-border bg-background p-5 shadow-xl" onClick={event => event.stopPropagation()}>
        <h2 id="pending-forks-title" className="font-semibold">Agent copies ready for you</h2>
        {offers.map(offer => {
          const job = jobFor(offer)
          return <div key={offer.id} className="space-y-2">
            <p className="text-sm">{offer.self_fork ? `Your copy of ${offer.agent.name} is ready.` : `${state.view?.people.find(person => person.user_id === offer.sender_id)?.name || 'A teammate'} shared a copy of ${offer.agent.name} with you.`}</p>
            <p className="text-xs text-muted-foreground">The whole conversation is included. Your new agent starts Not shared, using your own login. The original stays unchanged.</p>
            {job && running(job)
              ? <div role="status" className="space-y-1.5"><div className="h-1.5 overflow-hidden rounded-full bg-muted"><div className="h-full rounded-full bg-primary transition-all duration-500" style={{ width: `${job.percent}%` }} /></div><p className="text-sm">{job.line}</p></div>
              : <>
                {job?.stage === 'failed' && <p role="alert" className="text-sm text-destructive">{job.error}</p>}
                <div className="flex justify-end gap-2"><button type="button" className="rounded-md px-3 py-2 text-sm hover:bg-muted" onClick={() => { void decline(offer) }}>Decline</button><button type="button" className="rounded-md bg-primary px-3 py-2 text-sm text-primary-foreground" onClick={() => { void accept(offer) }}>{job?.stage === 'failed' ? 'Try again' : 'Accept copy'}</button></div>
              </>}
          </div>
        })}
        {active.filter(job => !job.fork_id).map(job => <div key={job.id} role="status" className="space-y-1.5">
          <p className="text-sm">Copying {job.name || 'an agent'} to your Mac</p>
          <div className="h-1.5 overflow-hidden rounded-full bg-muted"><div className="h-full rounded-full bg-primary transition-all duration-500" style={{ width: `${job.percent}%` }} /></div>
          <p className="text-xs text-muted-foreground">{job.line}</p>
        </div>)}
        {error && <p role="alert" className="text-sm text-destructive">{error}</p>}
        <div className="flex items-center justify-between gap-2">
          {busy ? <p className="text-xs text-muted-foreground">You can close this; copying keeps going and you'll get a notice when it's done.</p> : <span />}
          <button type="button" className="shrink-0 rounded-md px-3 py-2 text-sm hover:bg-muted" onClick={() => setOpen(false)}>Close</button>
        </div>
      </section>
    </div>, document.body)}
  </>
}
