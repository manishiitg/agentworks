import { useState } from 'react'
import { Button } from '../../components/ui/Button'
import { bullpenApi, type BullpenAgent, type BullpenUsage, type UsageReading } from '../../services/bullpenApi'

const exact = new Intl.NumberFormat()
const total = (c: { input_tokens: number; output_tokens: number }) => c.input_tokens + c.output_tokens

export function AgentUsage({ usage, unavailable, provider }: { usage?: BullpenUsage; unavailable?: boolean; provider?: string }) {
  if (unavailable) return <p className="text-xs text-muted-foreground">Usage could not be loaded.</p>
  if (!usage) return provider === 'cursor' || provider === 'agy' ? <p className="text-xs text-muted-foreground">Native usage is unavailable for this CLI.</p> : null
  const stale = Date.now() - new Date(usage.observed_at).getTime() > 2 * 60 * 60 * 1000
  return <div className="space-y-1 border-t border-border pt-3 text-xs">
    <p>Today’s hourly readings: <strong>{usage.available || total(usage.today) > 0 ? exact.format(total(usage.today)) : 'Not collected'}</strong> tokens</p>
    {usage.reporting && usage.available ? <>
      <p>This session: <strong>{exact.format(total(usage.session))}</strong> tokens · {usage.session.model || 'Model not reported'}</p>
      <p className="text-muted-foreground">Input {exact.format(usage.session.input_tokens)} · output {exact.format(usage.session.output_tokens)} · cached {exact.format(usage.session.cached_tokens)} (part of input)</p>
      {Boolean(usage.session.skipped_records) && <p className="text-muted-foreground">Some records skipped ({exact.format(usage.session.skipped_records!)}). Totals include identifiable recorded usage only.</p>}
      <p className="text-muted-foreground">From {usage.session.source}. {stale ? 'Reading is stale. ' : ''}Last read {new Date(usage.observed_at).toLocaleString()}.</p>
    </> : <p className="text-muted-foreground">{usage.reporting ? usage.reason === 'cap_reached' ? 'Native read cap reached. Check agentworks usage status.' : 'Native usage unavailable; no zero estimate is substituted.' : 'Reporting is off. Saved hourly history remains.'}</p>}
  </div>
}

export function BullpenUsageOverview({ agents, onChange }: { agents: BullpenAgent[]; onChange: () => void }) {
  const [readings, setReadings] = useState<UsageReading[]>([])
  const [before, setBefore] = useState<number>()
  const [more, setMore] = useState(false)
  const [loaded, setLoaded] = useState(false)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [confirmDelete, setConfirmDelete] = useState(false)
  const [copied, setCopied] = useState('')
  const [agentFilter, setAgentFilter] = useState('all')
  const [modelFilter, setModelFilter] = useState('all')
  const [group, setGroup] = useState<'hour' | 'day'>('day')
  const today = agents.reduce((n, a) => n + (a.usage ? total(a.usage.today) : 0), 0)
  const active = agents.filter(a => a.online && a.usage?.reporting && a.usage.available)
  const sessions = active.reduce((n, a) => n + total(a.usage!.session), 0)
  const zone = Intl.DateTimeFormat().resolvedOptions().timeZone
  const copy = async (command: string) => { try { await navigator.clipboard.writeText(command); setCopied(command) } catch { setError('Could not copy. Run the displayed command in your own terminal.') } }
  const load = async () => {
    setBusy(true); setError('')
    try { const page = await bullpenApi.usage(before); setReadings(prev => [...prev, ...page.readings]); setBefore(page.before); setMore(page.has_more); setLoaded(true) }
    catch { setError('Could not load usage history. Try again.') }
    finally { setBusy(false) }
  }
  const remove = async () => {
    setBusy(true); setError('')
    try { await bullpenApi.deleteUsage(); setReadings([]); setBefore(undefined); setMore(false); setLoaded(false); setConfirmDelete(false); onChange() }
    catch { setError('History deletion failed. Nothing is claimed deleted; try again.') }
    finally { setBusy(false) }
  }
  const models = [...new Set(readings.map(r => r.session.model || 'unknown'))]
  const visible = readings.filter(r => (agentFilter === 'all' || r.agent_id === agentFilter) && (modelFilter === 'all' || (r.session.model || 'unknown') === modelFilter))
  const buckets = new Map<string, { tokens: number; samples: number; label: string; time: number }>()
  for (const r of visible) {
    if (r.baseline || r.discontinuity) continue
    const d = new Date(r.observed_at)
    const key = group === 'day' ? `${d.getFullYear()}-${d.getMonth()}-${d.getDate()}` : `${d.getFullYear()}-${d.getMonth()}-${d.getDate()}-${d.getHours()}-${d.getTimezoneOffset()}`
    const value = buckets.get(key) || { tokens: 0, samples: 0, label: group === 'day' ? d.toLocaleDateString() : d.toLocaleString(undefined, { year: 'numeric', month: 'short', day: 'numeric', hour: 'numeric', timeZoneName: 'short' }), time: d.getTime() }
    value.tokens += total(r.delta); value.samples++; buckets.set(key, value)
  }
  const series = [...buckets.values()].sort((a, b) => a.time - b.time)
  const max = Math.max(1, ...series.map(b => b.tokens))
  return <section aria-label="Optional token usage" className="space-y-4 rounded-xl border border-border p-4">
    <div><h3 className="font-medium">Optional token usage</h3><p className="mt-1 text-xs text-muted-foreground">Native recorded numbers only. Reporting is off by default and controlled on your computer.</p></div>
    <div className="flex flex-wrap gap-x-6 gap-y-2 text-sm"><p>Your agents today: <strong>{agents.some(a => a.usage?.available) || today > 0 ? exact.format(today) : 'Not collected'}</strong></p><p>Connected session total: <strong>{active.length ? exact.format(sessions) : 'Not collected'}</strong></p><p className="text-xs text-muted-foreground">{zone} · {active.length} sessions with current numbers</p></div>
    <div className="flex flex-wrap items-center gap-2">
      <Button size="sm" variant="outline" onClick={() => void copy('agentworks usage on')}>Copy enable command</Button>
      <Button size="sm" variant="outline" onClick={() => void copy('agentworks usage off')}>Copy off command</Button>
      <Button size="sm" variant="outline" onClick={() => setConfirmDelete(true)} disabled={busy}>Delete saved history</Button>
      <code className="break-all text-xs">{copied || 'agentworks usage status'}</code>
    </div>
    <p className="text-xs leading-5 text-muted-foreground">Run on/off yourself in a terminal. Off stops collection and clears current reporting; history stays for up to 90 days until you delete it. Tokens today are tracked hourly readings, grouped by the local date they were taken. A reading may span midnight; the initial session baseline is not charged to its enabling hour.</p>
    {confirmDelete && <div role="alert" className="space-y-2 rounded border border-destructive/30 p-3 text-sm"><p>Delete all your saved usage history on this server? This cannot be undone. It does not change your computer’s opt-in setting.</p><Button size="sm" variant="destructive" disabled={busy} onClick={() => void remove()}>Delete history</Button><Button size="sm" variant="ghost" onClick={() => setConfirmDelete(false)}>Cancel</Button></div>}
    {error && <p role="alert" className="text-sm text-destructive">{error}</p>}
    <details><summary className="cursor-pointer text-sm text-primary">Hourly usage history</summary><div className="mt-4 space-y-3">
      <div className="flex flex-wrap gap-2"><Button size="sm" variant="outline" disabled={busy || loaded && !more || readings.length >= 25000} onClick={() => void load()}>{busy ? 'Loading…' : loaded ? 'Load earlier readings' : 'Load history'}</Button>
        <label className="text-xs">Agent <select value={agentFilter} onChange={e => setAgentFilter(e.target.value)} className="ml-1 rounded border border-border bg-background p-1"><option value="all">All agents</option>{agents.map(a => <option key={a.agent_id} value={a.agent_id}>{a.agent_name}</option>)}</select></label>
        <label className="text-xs">Model at reading <select value={modelFilter} onChange={e => setModelFilter(e.target.value)} className="ml-1 rounded border border-border bg-background p-1"><option value="all">All models</option>{models.map(m => <option key={m} value={m}>{m}</option>)}</select></label>
        <label className="text-xs">Group <select value={group} onChange={e => setGroup(e.target.value as 'hour' | 'day')} className="ml-1 rounded border border-border bg-background p-1"><option value="day">Local day</option><option value="hour">Local hour</option></select></label>
      </div>
      {loaded && <p className="text-xs text-muted-foreground">Showing {readings.length} hourly readings{more ? '; load earlier for more history' : ''}. Models describe the native model at reading time; an interval can include model changes. Gaps and counter resets are not estimated.</p>}
      <div aria-label="Usage over time" className="max-h-72 space-y-2 overflow-auto">{series.map(b => <div key={b.label + b.time} className="grid grid-cols-[8rem_1fr_6rem] items-center gap-2 text-xs"><span>{b.label}</span><div className="h-3 rounded bg-muted"><div className="h-3 rounded bg-primary" style={{ width: `${Math.max(1, b.tokens / max * 100)}%` }} /></div><span className="text-right">{exact.format(b.tokens)}</span></div>)}</div>
      {loaded && !series.length && <p className="text-xs text-muted-foreground">No tracked usage deltas in these readings yet. The first reading establishes a baseline.</p>}
    </div></details>
  </section>
}
