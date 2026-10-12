import { useEffect, useState } from 'react'
import { RefreshCw, Search } from 'lucide-react'
import { Button } from '../../components/ui/Button'
import { bullpenApi, bullpenError, type BullpenAdminAuditRow, type BullpenAdminPage } from '../../services/bullpenApi'

function when(value: string): string {
  const date = new Date(value)
  return Number.isNaN(date.getTime()) ? '' : date.toLocaleString()
}

// Read-only for administrators: who is on the server and what they run. Changes
// (disconnect, remove a name, make an agent private) are made through the
// bullpen_admin tool, each with a reason and an audit row.
export function AdminPanel() {
  const [page, setPage] = useState<BullpenAdminPage | null>(null)
  const [audit, setAudit] = useState<BullpenAdminAuditRow[]>([])
  const [query, setQuery] = useState('')
  const [cursors, setCursors] = useState<string[]>([''])
  const [error, setError] = useState('')
  const [attempt, setAttempt] = useState(0)
  const cursor = cursors[cursors.length - 1]

  useEffect(() => {
    const controller = new AbortController()
    setError('')
    Promise.all([bullpenApi.adminAgents(query.trim(), cursor, controller.signal), bullpenApi.adminAudit(controller.signal)]).then(([agents, rows]) => {
      if (controller.signal.aborted) return
      setPage(agents)
      setAudit(rows ?? [])
    }).catch(reason => { if (!controller.signal.aborted) setError(bullpenError(reason)) })
    return () => controller.abort()
  }, [attempt, query, cursor])

  return <section aria-label="Everyone on this server" className="space-y-6 border-t border-border pt-8">
    <div className="flex flex-wrap items-start justify-between gap-3">
      <div><h2 className="text-lg font-semibold">Everyone on this server</h2><p className="mt-1 text-sm text-muted-foreground">For administrators: every person and their agents, never messages. To disconnect a chat, remove a stale name or take an agent out of its groups, use the bullpen_admin tool; each change needs a reason and is recorded.</p></div>
      <Button variant="outline" size="sm" onClick={() => setAttempt(value => value + 1)}><RefreshCw className="mr-2 h-3.5 w-3.5" />Refresh</Button>
    </div>
    {error && <p role="alert" className="rounded-lg border border-destructive/30 p-4 text-sm text-destructive">{error}</p>}
    <div className="relative"><Search aria-hidden="true" className="absolute left-3 top-3 h-4 w-4 text-muted-foreground" /><input aria-label="Find a person or agent" value={query} onChange={event => { setQuery(event.target.value); setCursors(['']) }} placeholder="Search people and agent names" className="w-full rounded-lg border border-border bg-background py-2.5 pl-10 pr-3 text-sm outline-none focus-visible:ring-2 focus-visible:ring-ring" /></div>

    {!page ? <p role="status" className="py-8 text-center text-sm text-muted-foreground">Loading…</p> : !(page.people ?? []).length ? <p className="py-8 text-center text-sm text-muted-foreground">No one matches.</p> : <ul className="space-y-3">
      {(page.people ?? []).map(person => <li key={person.username} className="rounded-xl border border-border p-4">
        <p className="font-semibold">{person.username} <span className="text-xs font-normal text-muted-foreground">{person.role}{person.disabled && ' · disabled'} · {(person.agents ?? []).length} agent{(person.agents ?? []).length === 1 ? '' : 's'}</span></p>
        {(person.agents ?? []).length > 0 && <ul className="mt-3 divide-y divide-border">
          {(person.agents ?? []).map(agent => <li key={agent.agent_id} className="py-2 text-sm">
            <span className="break-all"><strong>{agent.agent_name}</strong>{(agent.groups ?? []).length > 0 && <span className="ml-2 rounded-full bg-muted px-2 py-0.5 text-xs">In {(agent.groups ?? []).join(', ')}</span>} · {agent.online ? 'Connected' : agent.attached ? 'Attached, offline' : 'Offline'}{agent.provider && ' · ' + agent.provider}</span>
            <span className="block text-xs text-muted-foreground">{agent.purpose || 'No purpose set.'} Created {when(agent.created_at)}</span>
          </li>)}
        </ul>}
      </li>)}
    </ul>}
    {page && <div className="flex items-center justify-between text-sm text-muted-foreground"><span>{page.total} {page.total === 1 ? 'person' : 'people'}</span>
      <span className="flex gap-2"><Button variant="outline" size="sm" disabled={cursors.length < 2} onClick={() => setCursors(list => list.slice(0, -1))}>Previous</Button><Button variant="outline" size="sm" disabled={!page.next_cursor} onClick={() => setCursors(list => [...list, page.next_cursor!])}>Next</Button></span></div>}

    <div className="space-y-2">
      <h3 className="font-medium">Recent administrator changes</h3>
      {!audit.length ? <p className="text-sm text-muted-foreground">No changes recorded.</p> : <ul className="divide-y divide-border rounded-xl border border-border">
        {audit.map((row, index) => <li key={row.at + index} className="p-3 text-sm"><strong>{row.admin}</strong> {row.event === 'agent_removed' ? 'removed a name of' : row.event === 'agent_unshared' ? 'took an agent out of its groups for' : 'disconnected an agent of'} <strong>{row.person}</strong> ({row.result}) · {when(row.at)}<span className="block break-words text-xs text-muted-foreground">Reason: {row.reason}</span></li>)}
      </ul>}
    </div>
  </section>
}
