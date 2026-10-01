import { useState } from 'react'
import { ScrollText } from 'lucide-react'
import { SettingsCard, SettingsCount } from '../../components/ui/SettingsCard'
import { Button } from '../../components/ui/Button'
import { Input } from '../../components/ui/Input'
import { auditPath, getUsage, listAudit, type GatewayAuditFilter } from './gatewayAdminApi'
import { ConsoleEmpty, ConsoleError, ConsoleLoading, ConsoleStale } from './gatewayConsoleShared'
import { codeClass, plural, tableClass, tdClass, thClass, useAttempt, useGatewayLoader } from './gatewayConsoleUtils'

const selectClass =
  'h-8 rounded-md border border-input bg-background px-2 text-xs shadow-sm focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring'

function outcomeDot(outcome: string): string {
  if (outcome === 'ok') return 'bg-green-500'
  if (outcome === 'denied') return 'bg-amber-500'
  return 'bg-red-500'
}

function formatTime(iso: string): string {
  const d = new Date(iso)
  return Number.isNaN(d.getTime()) ? iso : d.toLocaleString()
}

export function GatewayAuditPanel({ base }: { base: string }) {
  const [attempt, bump] = useAttempt()
  const [limit, setLimit] = useState(100)
  const [draft, setDraft] = useState({ user: '', group: '', client: '', connector: '', tool: '', decision: '', outcome: '', after: '', before: '' })
  const [filter, setFilter] = useState<GatewayAuditFilter>({})
  const { data, loading, error } = useGatewayLoader(async () => {
    const [audit, usage] = await Promise.all([listAudit(base, limit, filter), getUsage(base, filter)])
    return { events: audit.events, usage }
  }, attempt)

  function applyFilters() {
    setFilter({
      ...draft,
      after: draft.after ? new Date(`${draft.after}T00:00:00`).toISOString() : '',
      before: draft.before ? new Date(`${draft.before}T23:59:59.999`).toISOString() : '',
    })
    bump()
  }

  if (loading) return <ConsoleLoading label="Loading audit events…" />
  if (!data) return <ConsoleError message={error ?? 'Failed to load.'} onRetry={bump} />

  const events = data.events

  return (
    <div className="space-y-4" data-testid="gateway-audit">
      {error && <ConsoleStale message={error} onRetry={bump} />}
      <div className="grid gap-2 rounded-lg border border-border p-3 sm:grid-cols-3">
        {(['user', 'group', 'client', 'connector', 'tool'] as const).map((field) => (
          <Input key={field} aria-label={`Filter by ${field}`} placeholder={field[0].toUpperCase() + field.slice(1)} value={draft[field]}
            onChange={(event) => setDraft(current => ({ ...current, [field]: event.target.value }))} />
        ))}
        {(['decision', 'outcome'] as const).map((field) => (
          <select key={field} aria-label={`Filter by ${field}`} className={selectClass} value={draft[field]}
            onChange={(event) => setDraft(current => ({ ...current, [field]: event.target.value }))}>
            <option value="">Any {field}</option>
            {(field === 'decision' ? ['allow', 'deny'] : ['ok', 'denied', 'upstream_error']).map(value => <option key={value} value={value}>{value}</option>)}
          </select>
        ))}
        {(['after', 'before'] as const).map((field) => (
          <label key={field} className="flex items-center gap-2 text-xs text-muted-foreground">
            {field === 'after' ? 'From' : 'Through'}
            <Input type="date" aria-label={`Filter ${field}`} value={draft[field]}
              onChange={(event) => setDraft(current => ({ ...current, [field]: event.target.value }))} />
          </label>
        ))}
        <div className="flex items-center gap-2">
          <Button size="sm" onClick={applyFilters}>Apply filters</Button>
          <a className="text-xs text-primary underline" href={`${base}${auditPath(filter, undefined, 'csv')}`}>Export CSV</a>
          <a className="text-xs text-primary underline" href={`${base}${auditPath(filter, undefined, 'json')}`}>Export JSON</a>
        </div>
      </div>
      <SettingsCard title="Usage history" description="All matching calls.">
        <div className="grid gap-2 sm:grid-cols-4">
          {([['Calls', data.usage.Total], ['Allowed', data.usage.Allowed], ['Denied', data.usage.Denied], ['Upstream errors', data.usage.UpstreamErrors]] as const).map(([label, count]) => (
            <div key={label} className="rounded-md border border-border px-3 py-2">
              <div className="text-xs text-muted-foreground">{label}</div>
              <div className="text-lg font-semibold tabular-nums">{count}</div>
            </div>
          ))}
        </div>
        <p className="mt-2 text-xs text-muted-foreground">Average call duration: {data.usage.AvgDurationMs} ms</p>
        {data.usage.ByDay.length > 0 && <div className="mt-3 overflow-x-auto">
          <table className={tableClass}>
            <thead><tr><th className={thClass}>Day (UTC)</th><th className={thClass}>Calls</th><th className={thClass}>Denied</th><th className={thClass}>Upstream errors</th></tr></thead>
            <tbody>{data.usage.ByDay.slice(0, 14).map(day => <tr key={day.Key}><td className={tdClass}>{day.Key}</td><td className={tdClass}>{day.Count}</td><td className={tdClass}>{day.Denied}</td><td className={tdClass}>{day.UpstreamErrors}</td></tr>)}</tbody>
          </table>
        </div>}
        {data.usage.ByTool.length > 0 && <div className="mt-3 overflow-x-auto">
          <div className="mb-1 text-xs font-medium">Most used tools</div>
          <table className={tableClass}>
            <thead><tr><th className={thClass}>Tool</th><th className={thClass}>Calls</th><th className={thClass}>Denied</th></tr></thead>
            <tbody>{data.usage.ByTool.slice(0, 10).map(tool => <tr key={tool.Key}><td className={tdClass}><span className={codeClass}>{tool.Key}</span></td><td className={tdClass}>{tool.Count}</td><td className={tdClass}>{tool.Denied}</td></tr>)}</tbody>
          </table>
        </div>}
      </SettingsCard>
      <SettingsCard
        icon={<ScrollText className="h-4 w-4 text-primary" />}
        title="Audit log"
        count={<SettingsCount>{plural(events.length, 'event')}</SettingsCount>}
        description="Newest first. Group keys identify the group, not the individual."
        actions={
          <select
            aria-label="Event limit"
            className={selectClass}
            value={limit}
            onChange={(e) => {
              setLimit(Number(e.target.value))
              bump()
            }}
          >
            {[50, 100, 500].map((n) => (
              <option key={n} value={n}>
                Last {n}
              </option>
            ))}
          </select>
        }
      >
        {events.length === 0 ? (
          <ConsoleEmpty>No tool calls yet. Calls appear here as clients use the gateway.</ConsoleEmpty>
        ) : (
          <div className="overflow-x-auto">
          <table className={tableClass}>
            <thead>
              <tr>
                <th className={thClass}>Time</th>
                <th className={thClass}>User</th>
                <th className={thClass}>Group</th>
                <th className={thClass}>Client</th>
                <th className={thClass}>Tool</th>
                <th className={thClass}>Decision</th>
                <th className={thClass}>Outcome</th>
                <th className={thClass}>Duration</th>
              </tr>
            </thead>
            <tbody>
              {events.map((e) => (
                <tr key={e.ID}>
                  <td className={`${tdClass} whitespace-nowrap`}>{formatTime(e.Timestamp)}</td>
                  <td className={tdClass}>
                    <span className={codeClass}>{e.UserID || '—'}</span>
                  </td>
                  <td className={tdClass}>{e.GroupIDs?.join(', ') || '—'}</td>
                  <td className={tdClass}>{e.ClientID || '—'}</td>
                  <td className={tdClass}>
                    <span className={codeClass}>{e.PublicName}</span>
                  </td>
                  <td className={tdClass}>{e.Decision}</td>
                  <td className={tdClass}>
                    <span className="inline-flex items-center gap-1.5">
                      <span className={`h-2 w-2 rounded-full ${outcomeDot(e.Outcome)}`} aria-hidden />
                      {e.Outcome}
                    </span>
                    {e.ErrorText && (
                      <span className="block max-w-60 truncate text-muted-foreground" title={e.ErrorText}>
                        {e.ErrorText}
                      </span>
                    )}
                  </td>
                  <td className={tdClass}>{e.DurationMs} ms</td>
                </tr>
              ))}
            </tbody>
          </table>
          </div>
        )}
      </SettingsCard>
    </div>
  )
}
