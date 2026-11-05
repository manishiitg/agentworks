import { useEffect, useRef, useState } from 'react'
import { desktopRequest } from '../../services/desktopConnections'
import { Button } from '../../components/ui/Button'

type SharedAgent = { tab: string; audience: 'team' | ''; address?: string; can_change?: boolean; agentbridge_public?: boolean }
type AgentRow = { tab: string; name: string; purpose: string }
type Placement = { location: 'publishing' | 'server' }

/** AgentBridge on the Mac online tab (owner, 4 Nov mockup): agents, not people. Per agent, Only my agents | Public. Public
 * is its own setting (owner, 5 Nov: Public is AgentBridge only): any agent on the server can message it, and it is never
 * listed on anyone's website. Who can use it as a person stays the Team tab's choice; this tab never changes it. */
export function MacAgentBridge({ projectId, connection, crewName }: { projectId: string; connection: string; crewName: string }) {
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
    const current = shared.find(agent => agent.tab === tab)
    const others = shared.filter(agent => agent.tab !== tab)
    // Only the Public flag changes; an agent with no people audience and no Public leaves the list.
    const mine: SharedAgent[] = publicOn || current?.audience ? [{ ...(current || { tab, audience: '' }), agentbridge_public: publicOn }] : []
    const next = [...others, ...mine].map(({ tab, audience, can_change, agentbridge_public }) => ({ tab, audience, can_change: Boolean(can_change), agentbridge_public: Boolean(agentbridge_public) }))
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
  const copySetup = async () => {
    const line = `Connect to AgentWorks, then ask my ${JSON.stringify(crewName)} Crew's agent to help me.`
    try { await navigator.clipboard.writeText(line); setCopied('setup'); window.setTimeout(() => setCopied(''), 1500) }
    catch { setError('Copy this setup line into your coding app: ' + line) }
  }
  if (!rows) return null
  return <div aria-label="AgentBridge">
    {rows.map(row => {
      const agent = shared.find(item => item.tab === row.tab)
      const place = placements[row.tab]?.location
      return <div key={row.tab} className="flex flex-wrap items-center gap-2.5 border-t border-border py-2.5 pl-3 text-sm">
        <span title={row.purpose || undefined} className="min-w-0 truncate">{row.name}</span>
        {agent?.agentbridge_public && agent.address && <>
          <code className="min-w-0 truncate rounded bg-muted px-1.5 py-0.5 text-xs">agent:{agent.address}</code>
          <Button className="h-7 rounded-full px-3" size="sm" variant="outline" onClick={() => void navigator.clipboard?.writeText(`agent:${agent.address}`).then(() => { setCopied(row.tab); window.setTimeout(() => setCopied(''), 1500) })}>{copied === row.tab ? 'Copied' : 'Copy'}</Button>
        </>}
        {place === 'server' || place === 'publishing'
          ? <span className="ml-auto text-[12.5px] text-muted-foreground">{place === 'server' ? 'On the server · public there' : 'Moving to the server'}</span>
          : <div role="radiogroup" aria-label={`Who can message ${row.name} on AgentBridge`} className="ml-auto flex overflow-hidden rounded-full border border-border text-xs">
              {([[false, 'Only my agents'], [true, 'Public']] as const).map(([value, label]) => {
                const on = value === Boolean(agent?.agentbridge_public)
                return <button key={label} type="button" role="radio" aria-checked={on} disabled={busy} onClick={() => { if (!on) void choose(row.tab, value) }}
                  className={`px-2.5 py-0.5 ${on ? 'bg-primary text-primary-foreground' : 'text-muted-foreground hover:bg-muted'}`}>{label}</button>
              })}
            </div>}
      </div>
    })}
    {/* Your own coding apps can always message this Crew: one setup line to paste there (owner, 5 Nov: less text). */}
    <button type="button" className="border-t border-border py-2.5 pl-3 text-left text-xs text-primary hover:underline w-full" onClick={() => void copySetup()}>{copied === 'setup' ? 'Copied' : 'Copy setup line'}</button>
    {error && <p role="alert" className="pl-3 text-xs text-amber-600">{error}</p>}
  </div>
}
