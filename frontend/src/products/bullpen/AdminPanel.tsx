import { useEffect, useState } from 'react'
import { RefreshCw, Search } from 'lucide-react'
import { Button } from '../../components/ui/Button'
import { bullpenApi, bullpenError, sharingError, type BullpenAdminAuditRow, type BullpenAdminPage } from '../../services/bullpenApi'

function when(value: string): string {
  const date = new Date(value)
  return Number.isNaN(date.getTime()) ? '' : date.toLocaleString()
}

export function AdminPanel() {
  const [page, setPage] = useState<BullpenAdminPage | null>(null)
  const [audit, setAudit] = useState<BullpenAdminAuditRow[]>([])
  const [query, setQuery] = useState('')
  const [cursors, setCursors] = useState<string[]>([''])
  const [reason, setReason] = useState('')
  const [error, setError] = useState('')
  const [notice, setNotice] = useState('')
  const [busy, setBusy] = useState(false)
  const [attempt, setAttempt] = useState(0)
  const cursor = cursors[cursors.length - 1]

  useEffect(() => {
    const controller = new AbortController()
    setError('')
    Promise.all([bullpenApi.adminAgents(query.trim(), cursor, controller.signal), bullpenApi.adminAudit(controller.signal)]).then(([agents, rows]) => {
      if (controller.signal.aborted) return
      setPage(agents)
      setAudit(rows)
    }).catch(reason => { if (!controller.signal.aborted) setError(bullpenError(reason)) })
    return () => controller.abort()
  }, [attempt, query, cursor])

  async function change(action: () => Promise<void>, done: string) {
    setBusy(true); setNotice(''); setError('')
    try { await action(); setNotice(done); setReason(''); setAttempt(value => value + 1) }
    catch (failure) { setError(sharingError(failure)) }
    finally { setBusy(false) }
  }
  const reasonOk = reason.trim().length >= 3

  return <section aria-label="Administration" className="space-y-6">
    <div className="flex flex-wrap items-start justify-between gap-3">
      <div><h2 className="text-lg font-semibold">Administration</h2><p className="mt-1 text-sm text-muted-foreground">Every person and agent on this server. You see names, purposes and connection state only, never messages. Changes need a reason, are recorded with your name and are shown to the agent’s owner.</p></div>
      <Button variant="outline" size="sm" disabled={busy} onClick={() => setAttempt(value => value + 1)}><RefreshCw className="mr-2 h-3.5 w-3.5" />Refresh</Button>
    </div>
    {error && <p role="alert" className="rounded-lg border border-destructive/30 p-4 text-sm text-destructive">{error}</p>}
    {notice && <p role="status" className="text-sm text-muted-foreground">{notice}</p>}
    <div className="grid gap-3 sm:grid-cols-2">
      <div className="relative"><Search aria-hidden="true" className="absolute left-3 top-3 h-4 w-4 text-muted-foreground" /><input aria-label="Find a person or agent" value={query} onChange={event => { setQuery(event.target.value); setCursors(['']) }} placeholder="Search people and agent names" className="w-full rounded-lg border border-border bg-background py-2.5 pl-10 pr-3 text-sm outline-none focus-visible:ring-2 focus-visible:ring-ring" /></div>
      <input aria-label="Reason for the next change" value={reason} maxLength={200} onChange={event => setReason(event.target.value)} placeholder="Reason for the next change (required)" className="rounded-lg border border-border bg-background px-3 py-2.5 text-sm outline-none focus-visible:ring-2 focus-visible:ring-ring" />
    </div>

    {!page ? <p role="status" className="py-8 text-center text-sm text-muted-foreground">Loading…</p> : !page.people.length ? <p className="py-8 text-center text-sm text-muted-foreground">No one matches.</p> : <ul className="space-y-3">
      {page.people.map(person => <li key={person.username} className="rounded-xl border border-border p-4">
        <p className="font-semibold">{person.username} <span className="text-xs font-normal text-muted-foreground">{person.role}{person.disabled && ' · disabled'} · {person.agents.length} agent{person.agents.length === 1 ? '' : 's'}</span></p>
        {person.agents.length > 0 && <ul className="mt-3 divide-y divide-border">
          {person.agents.map(agent => <li key={agent.agent_id} className="flex flex-wrap items-center justify-between gap-3 py-2 text-sm">
            <span className="min-w-0 break-all"><strong>{agent.agent_name}</strong> · {agent.online ? 'Connected' : agent.attached ? 'Attached, offline' : 'Offline'}{agent.provider && ' · ' + agent.provider}<span className="block text-xs text-muted-foreground">{agent.purpose || 'No purpose set.'} Created {when(agent.created_at)}</span></span>
            <span className="flex gap-2">
              {agent.attachment_handle && <Button variant="outline" size="sm" disabled={busy || !reasonOk} aria-label={'Disconnect ' + agent.agent_name + ' of ' + person.username} onClick={() => void change(() => bullpenApi.adminCloseAttachment(agent.attachment_handle!, reason.trim()), 'Disconnected ' + agent.agent_name + '. Its owner can attach again.')}>Disconnect</Button>}
              <Button variant="outline" size="sm" disabled={busy || !reasonOk || agent.online} title={agent.online ? 'Disconnect it first; only a stale name can be removed.' : undefined} aria-label={'Remove ' + agent.agent_name + ' of ' + person.username} onClick={() => void change(() => bullpenApi.adminRemoveAgent(agent.agent_id, reason.trim()), 'Removed ' + agent.agent_name + '.')}>Remove name</Button>
            </span>
          </li>)}
        </ul>}
      </li>)}
    </ul>}
    {page && <div className="flex items-center justify-between text-sm text-muted-foreground"><span>{page.total} {page.total === 1 ? 'person' : 'people'}</span>
      <span className="flex gap-2"><Button variant="outline" size="sm" disabled={busy || cursors.length < 2} onClick={() => setCursors(list => list.slice(0, -1))}>Previous</Button><Button variant="outline" size="sm" disabled={busy || !page.next_cursor} onClick={() => setCursors(list => [...list, page.next_cursor!])}>Next</Button></span></div>}

    <div className="space-y-2">
      <h3 className="font-medium">Recent administrator changes</h3>
      {!audit.length ? <p className="text-sm text-muted-foreground">No changes recorded.</p> : <ul className="divide-y divide-border rounded-xl border border-border">
        {audit.map((row, index) => <li key={row.at + index} className="p-3 text-sm"><strong>{row.admin}</strong> {row.event === 'agent_removed' ? 'removed a name of' : 'disconnected an agent of'} <strong>{row.person}</strong> ({row.result}) · {when(row.at)}<span className="block break-words text-xs text-muted-foreground">Reason: {row.reason}</span></li>)}
      </ul>}
    </div>
  </section>
}
