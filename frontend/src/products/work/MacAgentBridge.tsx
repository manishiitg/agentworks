import { useEffect, useRef, useState } from 'react'
import api from '../../services/api'
import { desktopRequest } from '../../services/desktopConnections'
import { Button } from '../../components/ui/Button'
import { CrewCodingApps } from './CrewCodingApps'

type SharedAgent = { tab: string; audience: 'team' | 'everyone' | 'chosen'; people?: string[]; address?: string; can_change?: boolean }
type AgentRow = { tab: string; name: string; purpose: string; icon?: string }
type Placement = { location: 'publishing' | 'server' }

/** AgentBridge on the Mac online tab (owner, 4 Nov mockup): agents, not people. Per agent, Only my agents | Public,
 * where Public is the same shared-agent record as Team's Everyone (reviewer, 4 Nov): one record, so both tabs agree.
 * An agent shared with Team or chosen people only says so here; a click on this tab never drops or widens that share. */
export function MacAgentBridge({ projectId, connection, crewName, onTeam }: { projectId: string; connection: string; crewName: string; onTeam: () => void }) {
  const [rows, setRows] = useState<AgentRow[] | null>(null)
  const [shared, setShared] = useState<SharedAgent[]>([])
  const [placements, setPlacements] = useState<Record<string, Placement>>({})
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [copied, setCopied] = useState('')
  const busyRef = useRef(false)
  busyRef.current = busy
  const endpoint = `/${encodeURIComponent(connection)}/crew-sync/${encodeURIComponent(projectId)}`
  useEffect(() => {
    let live = true
    setRows(null); setShared([]); setPlacements({})
    const load = () => desktopRequest<{ tabs: AgentRow[]; agents: SharedAgent[]; placements?: Record<string, Placement> }>(endpoint, 'POST', { action: 'agents' })
      .then(data => { if (!live || busyRef.current) return; setRows(data.tabs || []); setShared(data.agents || []); setPlacements(data.placements || {}) })
      .catch(cause => { if (live) { setRows(current => current || []); setError(cause instanceof Error ? cause.message : "This Crew's agents could not be read.") } })
    void load()
    const timer = window.setInterval(() => { void load() }, 15000)
    window.addEventListener('focus', load)
    return () => { live = false; window.clearInterval(timer); window.removeEventListener('focus', load) }
  }, [endpoint])
  const choose = async (tab: string, publicOn: boolean) => {
    const next = shared.filter(agent => agent.tab !== tab).map(({ tab, audience, people, can_change }) => ({ tab, audience, people, can_change: Boolean(can_change) }))
    if (publicOn) next.push({ tab, audience: 'everyone', people: [], can_change: false })
    setBusy(true); setError('')
    try {
      const data = await desktopRequest<{ agents: SharedAgent[] }>(endpoint, 'POST', { action: 'share_agents', agents: next })
      setShared(data?.agents || [])
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : 'It was not saved. Try again.')
    } finally {
      setBusy(false)
    }
  }
  if (!rows) return null
  return <div aria-label="AgentBridge">
    {rows.map(row => {
      const agent = shared.find(item => item.tab === row.tab)
      const place = placements[row.tab]?.location
      return <div key={row.tab} className="flex flex-wrap items-center gap-2.5 border-t border-border py-2.5 pl-3 text-sm">
        <span aria-hidden="true">{row.icon?.trim() || '•'}</span>
        <span title={row.purpose || undefined} className="min-w-0 truncate">{row.name}</span>
        {agent?.audience === 'everyone' && agent.address && <>
          <code className="min-w-0 truncate rounded bg-muted px-1.5 py-0.5 text-xs">agent:{agent.address}</code>
          <Button className="h-7 rounded-full px-3" size="sm" variant="outline" onClick={() => void navigator.clipboard?.writeText(`agent:${agent.address}`).then(() => { setCopied(row.tab); window.setTimeout(() => setCopied(''), 1500) })}>{copied === row.tab ? 'Copied' : 'Copy'}</Button>
        </>}
        {place === 'server' || place === 'publishing'
          ? <span className="ml-auto text-[12.5px] text-muted-foreground">{place === 'server' ? 'On the server · public there' : 'Moving to the server'}</span>
          : agent && agent.audience !== 'everyone'
            ? <span className="ml-auto text-[12.5px] text-muted-foreground">{agent.audience === 'team' ? 'Shared with your team' : 'Shared with chosen people'} · <button type="button" className="text-primary hover:underline" onClick={onTeam}>change in Team</button></span>
            : <div role="radiogroup" aria-label={`Who can message ${row.name} on AgentBridge`} className="ml-auto flex overflow-hidden rounded-full border border-border text-xs">
              {([[false, 'Only my agents'], [true, 'Public']] as const).map(([value, label]) => {
                const on = value === (agent?.audience === 'everyone')
                return <button key={label} type="button" role="radio" aria-checked={on} disabled={busy} onClick={() => { if (!on) void choose(row.tab, value) }}
                  className={`px-2.5 py-0.5 ${on ? 'bg-primary text-primary-foreground' : 'text-muted-foreground hover:bg-muted'}`}>{label}</button>
              })}
            </div>}
      </div>
    })}
    <CrewCodingApps connection={connection} crewName={crewName} otherCrews={<OtherCrews projectId={projectId} />} />
    {error && <p role="alert" className="pl-3 text-xs text-amber-600">{error}</p>}
  </div>
}

/** This Mac's other Crews, which can always message this one: names from the Mac's own agent directory. */
function OtherCrews({ projectId }: { projectId: string }) {
  const [names, setNames] = useState<string[]>([])
  useEffect(() => {
    let live = true
    // The person's own Crews are "crew" rows; "crew_agent" rows are other people's shared agents.
    void api.get<{ agents?: { kind?: string; crew_id?: string; name?: string }[] }>('/api/agent-directory', { skipSessionContext: true }).then(({ data }) => {
      const seen = new Set<string>()
      for (const row of data.agents || []) if (row.kind === 'crew' && row.crew_id !== projectId && row.name) seen.add(row.name)
      if (live) setNames([...seen])
    }).catch(() => {})
    return () => { live = false }
  }, [projectId])
  if (!names.length) return null
  return <span>· your other Crews ({names.slice(0, 3).join(', ')}{names.length > 3 ? '…' : ''})</span>
}
