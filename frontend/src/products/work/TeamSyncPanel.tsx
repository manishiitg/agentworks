import { useEffect, useState } from 'react'
import { GitCompareArrows, RefreshCw } from 'lucide-react'
import { Button } from '../../components/ui/Button'
import { useChatStore } from '../../stores/useChatStore'
import { activateTab } from '../../utils/activateTab'
import { CHAT_FOCUS_COMPOSER_EVENT } from '../../utils/workspacePaneChat'
import { desktopRequest } from '../../services/desktopConnections'
import { teamError, teamRequest, type TeamClash, type TeamEvent, type TeamTarget } from './teamApi'

export function TeamSyncPanel({ target, name, onSummary, onAsk }: { target: TeamTarget; name?: string; onSummary?: (clashes: number, today: number) => void; onAsk?: () => void }) {
  const [clashes, setClashes] = useState<TeamClash[]>([])
  const [events, setEvents] = useState<TeamEvent[]>([])
  const [skipped, setSkipped] = useState<string[]>([])
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)
  const [version, setVersion] = useState(0)
  const [comparison, setComparison] = useState<{ path: string; diff: string }>()
  useEffect(() => {
    let active = true
    const load = async () => {
      try {
        const [a, b] = await Promise.all([teamRequest<{ clashes: TeamClash[] }>(target, '/clashes'), teamRequest<{ events: TeamEvent[] }>(target, '/events')])
        if (!active) return
        setClashes(a.clashes); setEvents(b.events); setError('')
        onSummary?.(a.clashes.length, b.events.filter(e => new Date(e.time).toDateString() === new Date().toDateString()).length)
        if (target.connection) {
          const local = await desktopRequest<{ skipped: string[] }>(`/${encodeURIComponent(target.connection)}/crew-teams/${encodeURIComponent(target.project)}/local`)
          if (active) setSkipped(local.skipped || [])
        }
      } catch (e) { if (active) setError(teamError(e)) }
    }
    void load(); const timer = window.setInterval(() => { if (!document.hidden) void load() }, 10000)
    return () => { active = false; window.clearInterval(timer) }
  }, [target.project, target.connection, version])
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
  return <div className="space-y-5 text-sm">
    <div className="flex items-center justify-between"><p className="text-xs text-muted-foreground">Latest arrival is current. Earlier versions stay in history.</p><button type="button" aria-label="Refresh Sync" onClick={() => setVersion(v => v + 1)} className="rounded p-1 hover:bg-muted"><RefreshCw className="h-4 w-4" /></button></div>
    <section aria-label="Open clashes" className="space-y-3"><h3 className="font-semibold">{clashes.length ? `${clashes.length} ${clashes.length === 1 ? 'clash' : 'clashes'}` : 'No open clashes'}</h3>
      {clashes.map(c => <article key={`${c.path}:${c.winning_revision}`} className="space-y-2 rounded-xl border border-amber-500/40 p-3">
        <p className="break-all font-medium">{c.path}</p><p className="text-xs text-muted-foreground">Current: {c.winning_author} · revision {c.winning_revision}<br />Earlier: {c.losing_author || 'No file'} · revision {c.losing_revision}</p>
        <div className="flex flex-wrap gap-2"><Button size="sm" disabled={busy} onClick={() => ask(c)}>Resolve with AI</Button><Button size="sm" variant="outline" disabled={busy} onClick={() => void compare(c)}><GitCompareArrows className="mr-1 h-3 w-3" />Compare</Button></div>
        <div className="flex flex-wrap gap-3 text-xs"><button disabled={busy} onClick={() => void resolve(c, 'current')} className="text-primary hover:underline">Keep current</button><button disabled={busy} onClick={() => void resolve(c, `revision:${c.losing_revision}`)} className="text-primary hover:underline">Restore earlier</button><button disabled={busy} onClick={() => void resolve(c, 'current')} className="text-primary hover:underline">Use the version I saved</button></div>
      </article>)}
    </section>
    {comparison && <section className="space-y-2"><div className="flex justify-between gap-2 text-xs"><span className="break-all">{comparison.path}</span><button onClick={() => setComparison(undefined)} className="text-primary">Close comparison</button></div><pre className="max-h-80 overflow-auto rounded-lg bg-muted p-3 text-[11px]">{comparison.diff || 'These versions have the same contents.'}</pre></section>}
    <section aria-label="Recent sync activity"><h3 className="mb-2 font-semibold">Recent</h3>{events.length ? events.slice(0, 100).map((e, i) => <div key={`${e.revision}:${e.path}:${i}`} className="border-b border-border py-2 text-xs"><p className="break-all">{e.path}</p><p className="mt-0.5 text-muted-foreground">{e.author} · {e.kind === 'clash' ? 'clash saved in history' : e.kind === 'removed' ? 'removed' : 'sent'} · {new Date(e.time).toLocaleString()}</p></div>) : <p className="text-xs text-muted-foreground">Changes will appear here when files sync.</p>}</section>
    {skipped.length > 0 && <section><h3 className="mb-1 font-semibold">Skipped private files</h3><ul className="space-y-1 text-xs text-muted-foreground">{skipped.map(path => <li key={path} className="break-all">{path}</li>)}</ul></section>}
    {error && <p role="alert" className="text-xs text-destructive">{error}</p>}
  </div>
}
