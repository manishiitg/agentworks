import { useEffect, useRef, useState } from 'react'
import { ChevronRight, GitCompareArrows, RefreshCw } from 'lucide-react'
import { Button } from '../../components/ui/Button'
import { useChatStore } from '../../stores/useChatStore'
import { activateTab } from '../../utils/activateTab'
import { CHAT_FOCUS_COMPOSER_EVENT } from '../../utils/workspacePaneChat'
import { desktopRequest } from '../../services/desktopConnections'
import { teamError, teamRequest, type TeamClash, type TeamEvent, type TeamTabSync, type TeamTarget } from './teamApi'
import { takeCrewSync } from './MacSharedAgents'

/** One sync round: one person's files sent (or removed) together, so a first upload is one row, not hundreds (owner,
 * 5 Nov). Events come newest first; a gap of over a minute, another person or another kind starts a new round.
 * Clashes stay one row each. */
type SyncRound = { author: string; kind: string; time: string; events: TeamEvent[] }
export function syncRounds(events: TeamEvent[]): SyncRound[] {
  const rounds: SyncRound[] = []
  for (const event of events) {
    const last = rounds[rounds.length - 1]
    const previous = last?.events[last.events.length - 1]
    if (last && event.kind !== 'clash' && last.kind === event.kind && last.author === event.author && previous && Math.abs(new Date(previous.time).getTime() - new Date(event.time).getTime()) <= 60_000) last.events.push(event)
    else rounds.push({ author: event.author, kind: event.kind, time: event.time, events: [event] })
  }
  return rounds
}

/** A sync round in progress, from the sync client itself (Mac status "progress"). */
export type SyncProgress = { direction: 'upload' | 'download'; files: number; bytes_done: number; bytes_total: number }
const progressPercent = (p: SyncProgress) => p.bytes_total > 0 ? Math.min(100, Math.round(p.bytes_done * 100 / p.bytes_total)) : 0

/** This Mac's copy of a team folder, from its sync client: the round in progress and the server-written line for it
 * (crew_team_desktop.go, worded like the website's). Polled every 2 s while a round runs, else every 10 s. */
type LocalSync = { line?: string; problem?: boolean; progress?: SyncProgress | null }
function useLocalSync(target?: TeamTarget, enabled = true): LocalSync | undefined {
  const [local, setLocal] = useState<LocalSync>()
  useEffect(() => {
    setLocal(undefined)
    if (!target?.connection || !enabled) return
    let active = true, timer = 0
    const load = async () => {
      let next: LocalSync | undefined
      try { next = await desktopRequest<LocalSync>(`/${encodeURIComponent(target.connection!)}/crew-teams/${encodeURIComponent(target.project)}/local`) } catch (e) { next = { line: `Can't sync: ${teamError(e)}`, problem: true } }
      if (!active) return
      setLocal(next)
      timer = window.setTimeout(() => void load(), next?.progress ? 2000 : 10000)
    }
    void load()
    return () => { active = false; window.clearTimeout(timer) }
  }, [target?.connection, target?.project, enabled])
  return local
}

const verb = (kind: string) => kind === 'clash' ? 'clash saved in history' : kind === 'removed' ? 'removed' : 'sent'
const clock = (time: string) => new Date(time).toLocaleString([], { month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit' })

/** The shared folder's sync: in the Mac app this Mac's own copy (from its sync client), elsewhere the server's line for
 * this person (server, from the Team tab reply); then clashes, recent rounds and skipped private files. */
export function TeamSyncPanel({ target, name, projectId, server }: { target: TeamTarget; name?: string; projectId?: string; server?: TeamTabSync }) {
  // Opened from the side panel's Shared folder row: scroll here once it is on screen.
  const root = useRef<HTMLDivElement>(null)
  useEffect(() => { if (projectId && takeCrewSync(projectId)) window.setTimeout(() => root.current?.scrollIntoView?.({ block: 'start', behavior: 'smooth' }), 300) }, [projectId])
  const [clashes, setClashes] = useState<TeamClash[]>([])
  const [events, setEvents] = useState<TeamEvent[]>([])
  const [skipped, setSkipped] = useState<string[]>([])
  // "Sync it anyway" (owner, 5 Nov): skipped single files can be chosen; chosen ones are synced on purpose.
  const [choosable, setChoosable] = useState<string[]>([])
  const [onPurpose, setOnPurpose] = useState<string[]>([])
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)
  const [version, setVersion] = useState(0)
  const [comparison, setComparison] = useState<{ path: string; diff: string }>()
  const local = useLocalSync(target)
  const progress = local?.progress || server?.progress || undefined
  // One line, written by the server for this copy: the Mac's own copy in the Mac app, else this server's.
  const status = local?.line ? { text: local.line, problem: Boolean(local.problem) } : server ? { text: server.line, problem: Boolean(server.problem) } : undefined
  const [details, setDetails] = useState(false)
  useEffect(() => {
    let active = true
    const load = async () => {
      try {
        const [a, b] = await Promise.all([teamRequest<{ clashes: TeamClash[] }>(target, '/clashes'), teamRequest<{ events: TeamEvent[] }>(target, '/events')])
        if (!active) return
        setClashes(a.clashes); setEvents(b.events); setError('')
        if (target.connection) {
          const local = await desktopRequest<{ skipped: string[]; skipped_files?: string[]; synced_on_purpose?: string[] }>(`/${encodeURIComponent(target.connection)}/crew-teams/${encodeURIComponent(target.project)}/local`)
          if (active) { setSkipped(local.skipped || []); setChoosable(local.skipped_files || []); setOnPurpose(local.synced_on_purpose || []) }
        }
      } catch (e) { if (active) setError(teamError(e)) }
    }
    void load(); const timer = window.setInterval(() => { if (!document.hidden) void load() }, 10000)
    return () => { active = false; window.clearInterval(timer) }
  }, [target.project, target.connection, version])
  const syncAnyway = async (path: string, on: boolean) => {
    if (!target.connection) return
    setBusy(true); setError('')
    try { await desktopRequest(`/${encodeURIComponent(target.connection)}/crew-teams/${encodeURIComponent(target.project)}/sync-anyway`, 'POST', { path, on }); setVersion(v => v + 1) }
    catch (e) { setError(teamError(e)) } finally { setBusy(false) }
  }
  const resolve = async (clash: TeamClash, keep: string) => {
    setBusy(true); setError('')
    try { await teamRequest(target, '/resolve', 'POST', { path: clash.path, keep, winning_revision: clash.winning_revision, transfer_id: crypto.randomUUID() }); setVersion(v => v + 1) }
    catch (e) { setError(teamError(e)) } finally { setBusy(false) }
  }
  const compare = async (clash: TeamClash) => {
    try { setComparison(await teamRequest(target, `/compare?${new URLSearchParams({ path: clash.path, left: String(clash.losing_revision), right: String(clash.winning_revision) })}`)) } catch (e) { setError(teamError(e)) }
  }
  const ask = (clash: TeamClash) => {
    const store = useChatStore.getState()
    const tabs = Object.values(store.chatTabs).filter(t => t.metadata?.agentProfileIsMainChat && !t.metadata?.isViewOnly)
    const tab = tabs.find(t => t.metadata?.agentProfileProjectId === (target.localProject || target.project)) || tabs[0]
    if (!tab) { setError('Open your own Main chat in this Crew, then ask it to compare these two versions.'); return }
    const prompt = `Help resolve the clash in ${name || 'my team'}: ${clash.path}. Compare versions ${clash.losing_revision} and ${clash.winning_revision} of the shared file, explain the differences, and suggest a merged version. Save the agreed version normally, then mark the clash resolved, keeping the current version.`
    const existing = store.getTabConfig(tab.tabId)?.inputText || ''
    store.setTabConfig(tab.tabId, { inputText: existing ? `${existing}\n\n${prompt}` : prompt })
    activateTab(tab.tabId); window.setTimeout(() => window.dispatchEvent(new CustomEvent(CHAT_FOCUS_COMPOSER_EVENT)), 50)
  }
  return <div ref={root} data-team-sync className="space-y-5 text-sm">
    <div className="flex items-center justify-between gap-2">{status ? <p role="status" className={`text-xs ${status.problem ? 'text-amber-600' : 'text-muted-foreground'}`}>{status.text}</p> : <span />}<button type="button" aria-label="Refresh Sync" onClick={() => setVersion(v => v + 1)} className="rounded p-1 hover:bg-muted"><RefreshCw className="h-4 w-4" /></button></div>
    {server?.last && <p className="text-xs text-muted-foreground">{server.last}</p>}
    {progress && <section aria-label="Sync in progress" className="space-y-1.5">
      <div role="progressbar" aria-label="Sync progress" aria-valuemin={0} aria-valuemax={100} aria-valuenow={progressPercent(progress)} className="h-1.5 overflow-hidden rounded-full bg-muted">
        <div className="h-full rounded-full bg-primary transition-[width] duration-500" style={{ width: `${progressPercent(progress)}%` }} />
      </div>
    </section>}
    {/* Clashes and Recent behind one toggle (approved mockup): the line above says the state. */}
    <button type="button" aria-expanded={details} onClick={() => setDetails(value => !value)} className="flex items-center gap-1 text-xs text-muted-foreground hover:text-foreground">
      <ChevronRight aria-hidden="true" className={`h-3 w-3 transition-transform ${details ? 'rotate-90' : ''}`} />{clashes.length ? `${clashes.length} ${clashes.length === 1 ? 'clash' : 'clashes'} · details` : 'Details'}
    </button>
    {details && <>
    <section aria-label="Open clashes" className="space-y-3"><h3 className="font-semibold">{clashes.length ? `${clashes.length} ${clashes.length === 1 ? 'clash' : 'clashes'}` : 'No open clashes'}</h3>
      {clashes.map(c => <article key={`${c.path}:${c.winning_revision}`} className="space-y-2 rounded-xl border border-amber-500/40 p-3">
        <p className="break-all font-medium">{c.path}</p><p className="text-xs text-muted-foreground">Current: {c.winning_author} · revision {c.winning_revision}<br />Earlier: {c.losing_author || 'No file'} · revision {c.losing_revision}</p>
        <div className="flex flex-wrap gap-2"><Button size="sm" disabled={busy} onClick={() => ask(c)}>Resolve with AI</Button><Button size="sm" variant="outline" disabled={busy} onClick={() => void compare(c)}><GitCompareArrows className="mr-1 h-3 w-3" />Compare</Button></div>
        <div className="flex flex-wrap gap-3 text-xs"><button disabled={busy} onClick={() => void resolve(c, 'current')} className="text-primary hover:underline">Keep current</button><button disabled={busy} onClick={() => void resolve(c, `revision:${c.losing_revision}`)} className="text-primary hover:underline">Restore earlier</button><button disabled={busy} onClick={() => void resolve(c, 'current')} className="text-primary hover:underline">Use the version I saved</button></div>
      </article>)}
    </section>
    {comparison && <section className="space-y-2"><div className="flex justify-between gap-2 text-xs"><span className="break-all">{comparison.path}</span><button onClick={() => setComparison(undefined)} className="text-primary">Close comparison</button></div><pre className="max-h-80 overflow-auto rounded-lg bg-muted p-3 text-[11px]">{comparison.diff || 'These versions have the same contents.'}</pre></section>}
    <section aria-label="Recent sync activity"><h3 className="mb-2 font-semibold">Recent</h3>{events.length ? syncRounds(events).slice(0, 100).map((round, i) => round.events.length === 1
      ? <div key={`${round.time}:${i}`} className="border-b border-border py-2 text-xs"><p className="break-all">{round.events[0].path}</p><p className="mt-0.5 text-muted-foreground">{round.author} · {verb(round.kind)} · {clock(round.time)}</p></div>
      : <details key={`${round.time}:${i}`} className="border-b border-border py-2 text-xs"><summary className="cursor-pointer">{round.author} {verb(round.kind)} {round.events.length} files · <span className="text-muted-foreground">{clock(round.time)}</span></summary>
        <ul className="mt-1 max-h-64 space-y-0.5 overflow-y-auto pl-4 text-muted-foreground">{round.events.map((e, j) => <li key={`${e.revision}:${e.path}:${j}`} className="break-all">{e.path}</li>)}</ul></details>)
      : <p className="text-xs text-muted-foreground">Changes will appear here when files sync.</p>}</section>
    </>}
    {(skipped.length > 0 || onPurpose.length > 0) && <section aria-label="Private-looking files" className="space-y-1.5">
      <h3 className="font-semibold">Skipped private files</h3>
      <p className="text-xs text-muted-foreground">These look like they may hold passwords or keys, so they stay on this Mac unless you choose to sync them. A synced file goes to everyone in the Team.</p>
      <ul className="space-y-1 text-xs">
        {skipped.map(path => <li key={path} className="flex items-center gap-2"><span className="min-w-0 flex-1 break-all text-muted-foreground">{path}</span>
          {choosable.includes(path) && <button type="button" disabled={busy} onClick={() => void syncAnyway(path, true)} className="shrink-0 text-primary hover:underline">Sync it anyway</button>}</li>)}
        {onPurpose.map(path => <li key={path} className="flex items-center gap-2"><span className="min-w-0 flex-1 break-all">{path} <span className="text-muted-foreground">· Synced on purpose</span></span>
          <button type="button" disabled={busy} onClick={() => void syncAnyway(path, false)} className="shrink-0 text-primary hover:underline">Stop syncing</button></li>)}
      </ul>
    </section>}
    {error && <p role="alert" className="text-xs text-destructive">{error}</p>}
  </div>
}
