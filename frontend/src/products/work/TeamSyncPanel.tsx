import { useEffect, useRef, useState } from 'react'
import { GitCompareArrows, RefreshCw } from 'lucide-react'
import { Button } from '../../components/ui/Button'
import { useChatStore } from '../../stores/useChatStore'
import { activateTab } from '../../utils/activateTab'
import { CHAT_FOCUS_COMPOSER_EVENT } from '../../utils/workspacePaneChat'
import { desktopRequest } from '../../services/desktopConnections'
import { teamError, teamRequest, type TeamClash, type TeamEvent, type TeamTarget } from './teamApi'
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
const mb = (bytes: number) => bytes >= 10 << 20 ? `${Math.round(bytes / (1 << 20))} MB` : bytes >= 1 << 20 ? `${(bytes / (1 << 20)).toFixed(1)} MB` : `${Math.max(1, Math.round(bytes / 1024))} KB`
export const progressLine = (p: SyncProgress) => `${p.direction === 'upload' ? 'Sending' : 'Receiving'} ${p.files} ${p.files === 1 ? 'file' : 'files'} · ${mb(p.bytes_done)} of ${mb(p.bytes_total)}`
const progressPercent = (p: SyncProgress) => p.bytes_total > 0 ? Math.min(100, Math.round(p.bytes_done * 100 / p.bytes_total)) : 0

/** This Mac's round in progress for a team folder, polled every 2 s while one runs (the web has none to show). */
function useSyncProgress(target?: TeamTarget, enabled = true): SyncProgress | undefined {
  const [progress, setProgress] = useState<SyncProgress>()
  useEffect(() => {
    setProgress(undefined)
    if (!target?.connection || !enabled) return
    let active = true, timer = 0
    const load = async () => {
      let next: SyncProgress | undefined
      try { next = (await desktopRequest<{ progress?: SyncProgress | null }>(`/${encodeURIComponent(target.connection!)}/crew-teams/${encodeURIComponent(target.project)}/local`)).progress || undefined } catch { next = undefined }
      if (!active) return
      setProgress(next)
      timer = window.setTimeout(() => void load(), next ? 2000 : 15000)
    }
    void load()
    return () => { active = false; window.clearTimeout(timer) }
  }, [target?.connection, target?.project, enabled])
  return progress
}

const verb = (kind: string) => kind === 'clash' ? 'clash saved in history' : kind === 'removed' ? 'removed' : 'sent'
const clock = (time: string) => new Date(time).toLocaleString([], { month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit' })

/** The shared folder in a few words for the Crew's side panel: open clashes, or how long ago the last change came. */
export function useTeamSyncSummary(target?: TeamTarget, enabled = true): string {
  const [line, setLine] = useState('')
  const progress = useSyncProgress(target, enabled)
  useEffect(() => {
    setLine('')
    if (!target || !enabled) return
    let active = true
    const load = async () => {
      try {
        const [a, b] = await Promise.all([teamRequest<{ clashes: TeamClash[] }>(target, '/clashes'), teamRequest<{ events: TeamEvent[] }>(target, '/events')])
        if (!active) return
        const minutes = b.events[0] ? Math.max(0, Math.round((Date.now() - new Date(b.events[0].time).getTime()) / 60_000)) : -1
        setLine(a.clashes.length ? `${a.clashes.length} ${a.clashes.length === 1 ? 'clash' : 'clashes'}`
          : minutes < 0 ? 'nothing synced yet' : minutes < 1 ? 'last change just now' : minutes < 60 ? `last change ${minutes} min ago` : `last change ${clock(b.events[0].time)}`)
      } catch { if (active) setLine('') }
    }
    void load(); const timer = window.setInterval(() => { if (!document.hidden) void load() }, 30000)
    return () => { active = false; window.clearInterval(timer) }
  }, [target?.project, target?.connection, target?.folderId, enabled])
  return progress ? `${progressLine(progress)} (${progressPercent(progress)}%)` : line
}

export function TeamSyncPanel({ target, name, projectId, onSummary, onAsk }: { target: TeamTarget; name?: string; projectId?: string; onSummary?: (clashes: number, today: number) => void; onAsk?: () => void }) {
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
  const progress = useSyncProgress(target)
  useEffect(() => {
    let active = true
    const load = async () => {
      try {
        const [a, b] = await Promise.all([teamRequest<{ clashes: TeamClash[] }>(target, '/clashes'), teamRequest<{ events: TeamEvent[] }>(target, '/events')])
        if (!active) return
        setClashes(a.clashes); setEvents(b.events); setError('')
        onSummary?.(a.clashes.length, b.events.filter(e => new Date(e.time).toDateString() === new Date().toDateString()).length)
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
    if (!tab) { setError('Open your own project’s Main chat, then ask it to compare these two revisions with team_sync.'); return }
    const prompt = `Help resolve the clash in ${name || 'my team'}: ${clash.path}. Compare revisions ${clash.losing_revision} and ${clash.winning_revision} with team_sync, explain the differences, and suggest a merged version. Save the agreed version normally, then mark the clash resolved with keep=current.`
    const existing = store.getTabConfig(tab.tabId)?.inputText || ''
    store.setTabConfig(tab.tabId, { inputText: existing ? `${existing}\n\n${prompt}` : prompt })
    activateTab(tab.tabId); onAsk?.(); window.setTimeout(() => window.dispatchEvent(new CustomEvent(CHAT_FOCUS_COMPOSER_EVENT)), 50)
  }
  return <div ref={root} data-team-sync className="space-y-5 text-sm">
    <div className="flex items-center justify-between"><p className="text-xs text-muted-foreground">Latest arrival is current. Earlier versions stay in history.</p><button type="button" aria-label="Refresh Sync" onClick={() => setVersion(v => v + 1)} className="rounded p-1 hover:bg-muted"><RefreshCw className="h-4 w-4" /></button></div>
    {progress && <section aria-label="Sync in progress" className="space-y-1.5">
      <p className="text-xs">{progressLine(progress)}</p>
      <div role="progressbar" aria-label="Sync progress" aria-valuemin={0} aria-valuemax={100} aria-valuenow={progressPercent(progress)} className="h-1.5 overflow-hidden rounded-full bg-muted">
        <div className="h-full rounded-full bg-primary transition-[width] duration-500" style={{ width: `${progressPercent(progress)}%` }} />
      </div>
    </section>}
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
