import { useEffect, useRef, useState } from 'react'
import { ChevronDown, ChevronUp, Download, ListFilter, RefreshCw, ScrollText, X } from 'lucide-react'
import { SettingsCard, SettingsCount } from '../../components/ui/SettingsCard'
import { Button } from '../../components/ui/Button'
import { Input } from '../../components/ui/Input'
import { auditPath, getAuditSettings, getUsage, listAudit, listConnectors, listGroups, listTools, listUsers, type GatewayAuditFilter } from './gatewayAdminApi'
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

export function GatewayAuditPanel({ base, tab = 'logs' }: { base: string; tab?: 'logs' | 'analysis' }) {
  const [attempt, bump] = useAttempt()
  const [limit, setLimit] = useState(100)
  const [filter, setFilter] = useState<GatewayAuditFilter>({})
  const [range, setRange] = useState('all')
  const [dates, setDates] = useState({ after: '', before: '' })
  const [moreFilters, setMoreFilters] = useState(false)
  const [directoryAttempt, refreshDirectory] = useAttempt()
  const { data: directory, error: directoryError } = useGatewayLoader(async () => {
    const [inventory, people, groups, tools] = await Promise.all([
      listConnectors(base), listUsers(base), listGroups(base), listTools(base),
    ])
    return { connectors: inventory.connectors, users: people.users, groups: groups.groups, tools: tools.tools }
  }, directoryAttempt)
  const { data, loading, error } = useGatewayLoader(async () => {
    const [audit, settings] = await Promise.all([
      listAudit(base, limit, filter), getAuditSettings(base),
    ])
    return { events: audit.events, settings }
  }, attempt)
  const { data: usage, error: usageError } = useGatewayLoader(
    async () => tab === 'analysis' ? getUsage(base, filter) : null,
    attempt * 2 + (tab === 'analysis' ? 1 : 0),
  )
  const seenClients = useRef(new Set<string>())
  useEffect(() => {
    for (const event of data?.events ?? []) if (event.ClientID) seenClients.current.add(event.ClientID)
  }, [data?.events])

  function timeFilter(nextRange: string, nextDates = dates): Pick<GatewayAuditFilter, 'after' | 'before'> {
    if (nextRange === 'custom') return {
      after: nextDates.after ? new Date(`${nextDates.after}T00:00:00`).toISOString() : '',
      before: nextDates.before ? new Date(`${nextDates.before}T23:59:59.999`).toISOString() : '',
    }
    const hours: Record<string, number> = { '1h': 1, '24h': 24, '7d': 168, '30d': 720 }
    return { after: hours[nextRange] ? new Date(Date.now() - hours[nextRange] * 3600000).toISOString() : '', before: '' }
  }

  function updateFilter(patch: GatewayAuditFilter) {
    setFilter(current => ({ ...current, ...timeFilter(range), ...patch }))
    bump()
  }

  function changeRange(value: string) {
    setRange(value)
    setFilter(current => ({ ...current, ...timeFilter(value) }))
    bump()
  }

  function changeDate(field: 'after' | 'before', value: string) {
    const nextDates = { ...dates, [field]: value }
    setDates(nextDates)
    setFilter(current => ({ ...current, ...timeFilter('custom', nextDates) }))
    bump()
  }

  function clearFilters() {
    setFilter({})
    setRange('all')
    setDates({ after: '', before: '' })
    bump()
  }

  function refresh() {
    updateFilter({})
    refreshDirectory()
  }

  if (loading) return <ConsoleLoading label="Loading audit events…" />
  if (!data) return <ConsoleError message={error ?? 'Failed to load.'} onRetry={bump} />

  const events = data.events
  const connectorNames = new Map((directory?.connectors ?? []).map(connector => [connector.ID, connector.Label || connector.Provider || connector.ID]))
  const userNames = new Map((directory?.users ?? []).map(user => [user.ID, user.Email || (user.ID === 'default' ? 'Local account' : user.ID)]))
  const groupNames = new Map((directory?.groups ?? []).map(group => [group.ID, group.Name]))
  for (const event of events) {
    if (event.UserID && !userNames.has(event.UserID)) userNames.set(event.UserID, event.UserID === 'default' ? 'Local account' : event.UserID)
    if (event.ConnectorID && !connectorNames.has(event.ConnectorID)) connectorNames.set(event.ConnectorID, event.ConnectorID)
    for (const id of event.GroupIDs ?? []) if (!groupNames.has(id)) groupNames.set(id, id)
  }
  const toolOptions = new Map((directory?.tools ?? [])
    .filter(tool => !filter.connector || tool.ConnectorID === filter.connector)
    .map(tool => [tool.PublicName, filter.connector ? tool.UpstreamName : `${connectorNames.get(tool.ConnectorID) || tool.ConnectorID} / ${tool.UpstreamName}`]))
  for (const event of events) if (!filter.connector || event.ConnectorID === filter.connector) {
    if (!toolOptions.has(event.PublicName)) toolOptions.set(event.PublicName, filter.connector ? event.UpstreamName : `${connectorNames.get(event.ConnectorID) || event.ConnectorID} / ${event.UpstreamName}`)
  }
  const clientOptions = new Map([...seenClients.current, ...events.map(event => event.ClientID || ''), filter.client || '']
    .filter(Boolean).map(id => [id, id === 'agentworks' ? 'AgentWorks' : id.startsWith('api-key:') ? `Group API key (${id.slice(8)})` : id]))
  const sortedOptions = (options: Map<string, string>) => [...options].sort((a, b) => a[1].localeCompare(b[1]))
  const advancedCount = [filter.group, filter.tool, filter.client, filter.decision].filter(Boolean).length
  const hasFilters = range !== 'all' || Object.values(filter).some(Boolean)

  return (
    <div className="space-y-4" data-testid="gateway-audit">
      {error && <ConsoleStale message={error} onRetry={bump} />}
      {data.settings && <div className="text-xs text-muted-foreground" role="status">
        {data.settings.enabled
          ? `${data.settings.provider === 'sqlite' ? 'SQLite' : data.settings.provider === 'clickhouse' ? 'ClickHouse' : 'Memory'}${data.settings.write_mode === 'async' ? ' · Async' : ''}${data.settings.retention_seconds ? ` · ${data.settings.retention_seconds / 3600}h retention` : ''}`
          : 'Auditing is off. New calls are not recorded.'}
      </div>}
      {data.settings?.enabled && data.settings.write_healthy === false && <p role="alert" className="text-xs text-destructive">Audit writes are delayed; queued events are awaiting retry.</p>}
      {directoryError && <ConsoleStale message="Filter options could not be refreshed." onRetry={refreshDirectory} />}
      <div className="space-y-2" aria-label="Audit filters">
        <div className="flex flex-wrap items-center gap-2">
          <select aria-label="Time range" className={`${selectClass} min-w-28 flex-1`} value={range} onChange={event => changeRange(event.target.value)}>
            <option value="all">Any time</option>
            <option value="1h">Last hour</option>
            <option value="24h">Last 24 hours</option>
            {(data.settings?.retention_seconds ?? 0) >= 604800 && <option value="7d">Last 7 days</option>}
            {(data.settings?.retention_seconds ?? 0) >= 2592000 && <option value="30d">Last 30 days</option>}
            <option value="custom">Custom dates…</option>
          </select>
          {([
            { key: 'user', label: 'User', all: 'All users', options: userNames },
            { key: 'connector', label: 'MCP server', all: 'All MCPs', options: connectorNames },
            { key: 'outcome', label: 'Result', all: 'All results', options: new Map([['ok', 'Success'], ['denied', 'Blocked'], ['upstream_error', 'Failed']]) },
          ] as const).map(field => <select key={field.key} aria-label={field.label} className={`${selectClass} min-w-28 flex-1`} value={filter[field.key] || ''}
            onChange={event => updateFilter({ [field.key]: event.target.value, ...(field.key === 'connector' ? { tool: '' } : {}) })}>
            <option value="">{field.all}</option>
            {sortedOptions(field.options).map(([id, name]) => <option key={id} value={id}>{name}</option>)}
          </select>)}
          <Button size="sm" variant={moreFilters ? 'secondary' : 'outline'} aria-expanded={moreFilters} aria-controls="audit-more-filters" onClick={() => setMoreFilters(value => !value)}>
            <ListFilter className="mr-1.5 h-3.5 w-3.5" /> More{advancedCount > 0 ? ` (${advancedCount})` : ''}
            {moreFilters ? <ChevronUp className="ml-1 h-3 w-3" /> : <ChevronDown className="ml-1 h-3 w-3" />}
          </Button>
          <Button size="sm" variant="ghost" aria-label="Refresh audit log" title="Refresh" onClick={refresh}><RefreshCw className="h-3.5 w-3.5" /></Button>
        </div>
        {range === 'custom' && <div className="flex flex-wrap items-center gap-2">
          {(['after', 'before'] as const).map(field => <label key={field} className="flex items-center gap-2 text-xs text-muted-foreground">
            {field === 'after' ? 'From' : 'To'}
            <Input type="date" aria-label={field === 'after' ? 'Start date' : 'End date'} value={dates[field]} onChange={event => changeDate(field, event.target.value)} />
          </label>)}
        </div>}
        {moreFilters && <div id="audit-more-filters" className="grid grid-cols-1 gap-2 rounded-md bg-muted/20 p-2 sm:grid-cols-2">
          {([
            { key: 'group', label: 'Group', all: 'All groups', options: groupNames },
            { key: 'tool', label: 'Tool', all: 'All tools', options: toolOptions },
            { key: 'client', label: 'Calling app', all: 'All apps', options: clientOptions },
            { key: 'decision', label: 'Permission decision', all: 'Any decision', options: new Map([['allow', 'Allowed'], ['deny', 'Denied']]) },
          ] as const).map(field => <label key={field.key} className="space-y-1 text-xs text-muted-foreground">{field.label}
            <select aria-label={field.label} className={`${selectClass} block w-full`} value={filter[field.key] || ''} onChange={event => updateFilter({ [field.key]: event.target.value })}>
              <option value="">{field.all}</option>
              {sortedOptions(field.options).map(([id, name]) => <option key={id} value={id}>{name}</option>)}
            </select>
          </label>)}
        </div>}
        <div className="flex items-center justify-between gap-2">
          <div>{hasFilters && <Button size="sm" variant="ghost" className="h-7 px-1.5 text-xs" onClick={clearFilters}><X className="mr-1 h-3 w-3" /> Clear filters</Button>}</div>
          <details className="relative text-xs">
            <summary className="flex cursor-pointer list-none items-center gap-1.5 rounded-md px-2 py-1 text-muted-foreground hover:bg-muted hover:text-foreground"><Download className="h-3.5 w-3.5" /> Export</summary>
            <div className="absolute right-0 z-10 mt-1 w-28 rounded-md border border-border bg-popover p-1 shadow-md">
              <a className="block rounded px-2 py-1.5 hover:bg-muted" href={`${base}${auditPath(filter, undefined, 'csv')}`}>CSV</a>
              <a className="block rounded px-2 py-1.5 hover:bg-muted" href={`${base}${auditPath(filter, undefined, 'json')}`}>JSON</a>
            </div>
          </details>
        </div>
      </div>
      {tab === 'analysis' && <div role="tabpanel" aria-label="Audit analysis">
      {usageError && (usage ? <ConsoleStale message={usageError} onRetry={bump} /> : <ConsoleError message={usageError} onRetry={bump} />)}
      {!usage && !usageError && <ConsoleLoading label="Loading analysis…" />}
      {usage && <SettingsCard title="Usage history" description="All matching calls.">
        <div className="grid gap-2 sm:grid-cols-4">
          {([['Calls', usage.Total], ['Allowed', usage.Allowed], ['Denied', usage.Denied], ['Upstream errors', usage.UpstreamErrors]] as const).map(([label, count]) => (
            <div key={label} className="rounded-md border border-border px-3 py-2">
              <div className="text-xs text-muted-foreground">{label}</div>
              <div className="text-lg font-semibold tabular-nums">{count}</div>
            </div>
          ))}
        </div>
        <p className="mt-2 text-xs text-muted-foreground">Average call duration: {usage.AvgDurationMs} ms</p>
        {usage.ByDay.length > 0 && <div className="mt-3 overflow-x-auto">
          <table className={tableClass}>
            <thead><tr><th className={thClass}>Day (UTC)</th><th className={thClass}>Calls</th><th className={thClass}>Denied</th><th className={thClass}>Upstream errors</th></tr></thead>
            <tbody>{usage.ByDay.slice(0, 14).map(day => <tr key={day.Key}><td className={tdClass}>{day.Key}</td><td className={tdClass}>{day.Count}</td><td className={tdClass}>{day.Denied}</td><td className={tdClass}>{day.UpstreamErrors}</td></tr>)}</tbody>
          </table>
        </div>}
        {usage.ByTool.length > 0 && <div className="mt-3 overflow-x-auto">
          <div className="mb-1 text-xs font-medium">Most used tools</div>
          <table className={tableClass}>
            <thead><tr><th className={thClass}>Tool</th><th className={thClass}>Calls</th><th className={thClass}>Denied</th></tr></thead>
            <tbody>{usage.ByTool.slice(0, 10).map(tool => <tr key={tool.Key}><td className={tdClass}><span className={codeClass}>{tool.Key}</span></td><td className={tdClass}>{tool.Count}</td><td className={tdClass}>{tool.Denied}</td></tr>)}</tbody>
          </table>
        </div>}
      </SettingsCard>}
      </div>}
      {tab === 'logs' && <div role="tabpanel" aria-label="Audit logs"><SettingsCard
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
          <ConsoleEmpty>{data.settings?.enabled === false ? 'Audit collection is off.' : hasFilters ? 'No calls match these filters.' : 'No tool calls yet.'}</ConsoleEmpty>
        ) : (
          <div className="overflow-x-auto">
          <table className={tableClass}>
            <thead>
              <tr>
                <th className={thClass}>Time</th>
                <th className={thClass}>User</th>
                <th className={thClass}>Group</th>
                <th className={thClass} title="App or credential calling Vault">Calling app</th>
                <th className={thClass}>MCP server</th>
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
                    <span title={e.UserID}>{userNames.get(e.UserID) || e.UserID || '—'}</span>
                  </td>
                  <td className={tdClass} title={e.GroupIDs?.join(', ')}>{e.GroupIDs?.map(id => groupNames.get(id) || id).join(', ') || '—'}</td>
                  <td className={tdClass} title={e.ClientID}>{e.ClientID === 'agentworks' ? 'AgentWorks' : e.ClientID || '—'}</td>
                  <td className={tdClass} title={e.ConnectorID}>{connectorNames.get(e.ConnectorID) || e.ConnectorID || '—'}</td>
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
      </SettingsCard></div>}
    </div>
  )
}
