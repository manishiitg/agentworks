import { Fragment, useEffect, useRef, useState } from 'react'
import { ChevronDown, ChevronUp, Download, ListFilter, RefreshCw, ScrollText, X } from 'lucide-react'
import { SettingsCard, SettingsCount } from '../../components/ui/SettingsCard'
import { Button } from '../../components/ui/Button'
import { Input } from '../../components/ui/Input'
import { auditPath, getAuditSettings, getUsage, listAudit, listConnectors, listGroups, listTools, listUsers, type GatewayAuditFilter } from './gatewayAdminApi'
import { ConsoleEmpty, ConsoleError, ConsoleLoading, ConsoleStale } from './gatewayConsoleShared'
import { codeClass, plural, tableClass, tdClass, thClass, useAttempt, useGatewayLoader } from './gatewayConsoleUtils'

const selectClass =
  'h-8 rounded-md border border-input bg-background px-2 text-xs shadow-sm focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring'

function callResult(outcome: string, decision: string) {
  if (outcome === 'denied' || decision === 'deny') return { label: 'Blocked', style: 'bg-amber-500/10 text-amber-600 dark:text-amber-400' }
  if (outcome === 'ok') return { label: 'Success', style: 'bg-emerald-500/10 text-emerald-600 dark:text-emerald-400' }
  return { label: 'Failed', style: 'bg-destructive/10 text-destructive' }
}

function callingApp(id: string) {
  if (id === 'agentworks') return 'AgentWorks'
  if (id === 'agentworks-vault-builder') return 'Vault builder'
  if (id.startsWith('api-key:')) return `Group API key (${id.slice(8)})`
  return id || '—'
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
  const [expandedEvent, setExpandedEvent] = useState<string | null>(null)
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
    .filter(Boolean).map(id => [id, callingApp(id)]))
  const sortedOptions = (options: Map<string, string>) => [...options].sort((a, b) => a[1].localeCompare(b[1]))
  const advancedCount = [filter.group, filter.tool, filter.client, filter.decision].filter(Boolean).length
  const hasFilters = range !== 'all' || Object.values(filter).some(Boolean)

  return (
    <div className="space-y-4" data-testid="gateway-audit">
      {error && <ConsoleStale message={error} onRetry={bump} />}
      {data.settings && <div className="text-xs text-muted-foreground" role="status" title={`${data.settings.provider} · ${data.settings.write_mode}`}>
        {data.settings.enabled
          ? data.settings.retention_seconds ? `Calls retained for ${data.settings.retention_seconds / 3600} hours` : 'Audit collection enabled'
          : 'Auditing is off. New calls are not recorded.'}
      </div>}
      {data.settings?.enabled && data.settings.write_healthy === false && <p role="alert" className="text-xs text-destructive">Audit writes are delayed; queued events are awaiting retry.</p>}
      {directoryError && <ConsoleStale message="Filter options could not be refreshed." onRetry={refreshDirectory} />}
      <div className="space-y-2" aria-label="Audit filters">
        <div className="flex flex-wrap items-center gap-2">
          <select aria-label="Time range" className={`${selectClass} min-w-28 sm:w-44`} value={range} onChange={event => changeRange(event.target.value)}>
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
          ] as const).map(field => <select key={field.key} aria-label={field.label} className={`${selectClass} min-w-28 sm:w-44`} value={filter[field.key] || ''}
            onChange={event => updateFilter({ [field.key]: event.target.value, ...(field.key === 'connector' ? { tool: '' } : {}) })}>
            <option value="">{field.all}</option>
            {sortedOptions(field.options).map(([id, name]) => <option key={id} value={id}>{name}</option>)}
          </select>)}
          <Button size="sm" variant={moreFilters ? 'secondary' : 'outline'} aria-expanded={moreFilters} aria-controls="audit-more-filters" onClick={() => setMoreFilters(value => !value)}>
            <ListFilter className="mr-1.5 h-3.5 w-3.5" /> More{advancedCount > 0 ? ` (${advancedCount})` : ''}
            {moreFilters ? <ChevronUp className="ml-1 h-3 w-3" /> : <ChevronDown className="ml-1 h-3 w-3" />}
          </Button>
          <Button size="sm" variant="ghost" aria-label="Refresh audit log" title="Refresh" onClick={refresh}><RefreshCw className="h-3.5 w-3.5" /></Button>
          <details className="relative ml-auto text-xs">
            <summary className="flex cursor-pointer list-none items-center gap-1.5 rounded-md px-2 py-1 text-muted-foreground hover:bg-muted hover:text-foreground"><Download className="h-3.5 w-3.5" /> Export</summary>
            <div className="absolute right-0 z-10 mt-1 w-28 rounded-md border border-border bg-popover p-1 shadow-md">
              <a className="block rounded px-2 py-1.5 hover:bg-muted" href={`${base}${auditPath(filter, undefined, 'csv')}`}>CSV</a>
              <a className="block rounded px-2 py-1.5 hover:bg-muted" href={`${base}${auditPath(filter, undefined, 'json')}`}>JSON</a>
            </div>
          </details>
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
        {hasFilters && <Button size="sm" variant="ghost" className="h-7 px-1.5 text-xs" onClick={clearFilters}><X className="mr-1 h-3 w-3" /> Clear filters</Button>}

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
        title="Recent calls"
        count={<SettingsCount>{plural(events.length, 'call')}</SettingsCount>}
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
          <table className={`${tableClass} min-w-[680px]`}>
            <thead><tr>
              <th className={thClass}>Time</th>
              <th className={thClass}>User</th>
              <th className={thClass}>MCP server</th>
              <th className={thClass}>Tool</th>
              <th className={thClass}>Result</th>
              <th className={`${thClass} text-right`}>Duration</th>
              <th className={thClass}><span className="sr-only">Details</span></th>
            </tr></thead>
            <tbody>{events.map(e => {
              const result = callResult(e.Outcome, e.Decision)
              const toolName = e.UpstreamName || directory?.tools.find(tool => tool.PublicName === e.PublicName)?.UpstreamName || e.PublicName.split('__').at(-1) || e.PublicName
              const expanded = expandedEvent === e.ID
              const timestamp = new Date(e.Timestamp)
              return <Fragment key={e.ID}>
                <tr className="hover:bg-muted/30">
                  <td className={`${tdClass} whitespace-nowrap`} title={formatTime(e.Timestamp)}>
                    <div className="tabular-nums">{Number.isNaN(timestamp.getTime()) ? e.Timestamp : timestamp.toLocaleTimeString()}</div>
                    <div className="mt-0.5 text-[11px] text-muted-foreground">{Number.isNaN(timestamp.getTime()) ? '' : timestamp.toLocaleDateString()}</div>
                  </td>
                  <td className={tdClass}><span className="block max-w-48 truncate" title={e.UserID}>{userNames.get(e.UserID) || e.UserID || (e.ClientID?.startsWith('api-key:') ? 'Group API key' : '—')}</span></td>
                  <td className={tdClass}><span className="block max-w-56 truncate" title={e.ConnectorID}>{connectorNames.get(e.ConnectorID) || e.ConnectorID || '—'}</span></td>
                  <td className={tdClass}><span className="block max-w-72 truncate font-medium" title={e.PublicName}>{toolName}</span></td>
                  <td className={tdClass}><span className={`inline-flex rounded-full px-2 py-0.5 text-[11px] font-medium ${result.style}`}>{result.label}</span></td>
                  <td className={`${tdClass} whitespace-nowrap text-right tabular-nums`}>{e.DurationMs} ms</td>
                  <td className={tdClass}><Button variant="ghost" size="icon" className="h-7 w-7" aria-label={`${expanded ? 'Hide' : 'View'} details for ${toolName} at ${e.Timestamp}`} aria-expanded={expanded} aria-controls={`audit-event-${e.ID}`} onClick={() => setExpandedEvent(expanded ? null : e.ID)}>
                    {expanded ? <ChevronUp className="h-3.5 w-3.5" /> : <ChevronDown className="h-3.5 w-3.5" />}
                  </Button></td>
                </tr>
                {expanded && <tr id={`audit-event-${e.ID}`}><td colSpan={7} className="border-b border-border bg-muted/20 px-4 py-3">
                  <dl className="grid gap-x-6 gap-y-3 text-xs sm:grid-cols-2 lg:grid-cols-4">
                    <div><dt className="text-muted-foreground">Groups</dt><dd className="mt-1">{e.GroupIDs?.map(id => groupNames.get(id) || id).join(', ') || '—'}</dd></div>
                    <div><dt className="text-muted-foreground">Calling app</dt><dd className="mt-1" title={e.ClientID}>{callingApp(e.ClientID || '')}</dd></div>
                    <div><dt className="text-muted-foreground">Permission decision</dt><dd className="mt-1">{e.Decision === 'allow' ? 'Allowed' : e.Decision === 'deny' ? 'Denied' : e.Decision}</dd></div>
                    <div><dt className="text-muted-foreground">Outcome</dt><dd className="mt-1">{e.Outcome}</dd></div>
                    <div className="sm:col-span-2 lg:col-span-4"><dt className="text-muted-foreground">Tool ID</dt><dd className="mt-1 break-all font-mono">{e.PublicName}</dd></div>
                  </dl>
                  <div className="mt-3 grid gap-2 md:grid-cols-2">
                    {(['Input', 'Output'] as const).map(kind => <details key={kind} className="min-w-0 rounded-md border border-border bg-background">
                      <summary className="cursor-pointer px-3 py-2 text-xs font-medium">{kind === 'Input' ? 'Input arguments' : 'Tool output'}{e[`${kind}Truncated`] && <span className="ml-2 text-amber-600">Truncated</span>}</summary>
                      {Object.hasOwn(e, kind) ? <pre className="max-h-80 overflow-auto border-t border-border p-3 text-[11px] leading-relaxed">{JSON.stringify(e[kind], null, 2)}</pre> : <p className="border-t border-border px-3 py-2 text-xs text-muted-foreground">{kind === 'Output' && e.Outcome === 'denied' ? 'Tool was not called.' : 'Not recorded for this call.'}</p>}
                    </details>)}
                  </div>
                  {e.ErrorText && <p className="mt-3 break-words text-destructive">{e.ErrorText}</p>}
                  {!e.UserID && e.ClientID?.startsWith('api-key:') && <p className="mt-3 text-muted-foreground">This call used a group key; no individual user was identified.</p>}
                </td></tr>}
              </Fragment>
            })}</tbody>
          </table>
          </div>
        )}
      </SettingsCard></div>}
    </div>
  )
}
