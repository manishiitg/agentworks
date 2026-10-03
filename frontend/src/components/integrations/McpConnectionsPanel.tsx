import { useEffect, useRef, useState, type ReactNode } from 'react'
import { Loader2, MoreHorizontal, Plus, RefreshCw, Search } from 'lucide-react'
import { Button } from '../ui/Button'
import { Input } from '../ui/Input'
import IconPopover from '../ui/IconPopover'
import ConnectionIcon from '../connectors/ConnectionIcon'
import { brandSlugFor } from '../connectors/brandSlug'
import { GROUP_ORDER } from '../connectors/catalog'
import { WorkspaceViewTabs } from '../workflow/WorkspaceViewTabs'
import { McpServerHeader } from './McpServerHeader'
import { McpToolCard } from './McpToolCard'

export interface McpToolRow {
  id: string; name: string; description?: string; status?: string
  schema?: string | null; rawSchema?: Record<string, unknown>; details?: ReactNode
}
export interface McpAction {
  label: string; ariaLabel?: string; icon?: ReactNode; disabled?: boolean; destructive?: boolean
  run: () => void | Promise<unknown>
}
export interface McpConnectionRow {
  id: string; name: string; source?: string; status: string; statusDot?: string
  toolCount?: number; tools?: McpToolRow[]; loadTools?: () => Promise<McpToolRow[]>
  toolsLabel?: (open: boolean) => string; toolsNotice?: ReactNode; settings?: ReactNode
  actions?: McpAction[]; controls?: ReactNode
  selection?: { label: string; checked: boolean; disabled?: boolean; change: () => void | Promise<unknown> }
}
export interface McpCatalogRow {
  id: string; name: string; description?: string; category?: string
  connect?: McpAction; connectControl?: ReactNode; testId?: string
  batch?: { id: string; name: string; connect: (ids: string[]) => void | Promise<unknown> }
}
export interface McpPanelNotice { message: string; retry?: () => void; tone?: 'error' | 'info' }

/** The sole MCP browser UI. Adapters provide scoped data and authorized callbacks, never layouts. */
export function McpConnectionsPanel({ servers, catalog = [], view, loading = false, notices = [], refresh, addCustom, help, children, searchTestId, headerActions, showTools = true }: {
  servers: McpConnectionRow[]; catalog?: McpCatalogRow[]; view?: 'connected' | 'available'
  loading?: boolean; notices?: McpPanelNotice[]; refresh?: () => void
  addCustom?: McpAction; help?: ReactNode; children?: ReactNode; searchTestId?: string; headerActions?: ReactNode; showTools?: boolean
}) {
  const [tab, setTab] = useState<'connected' | 'available'>('connected')
  const activeView = view ?? tab
  const [query, setQuery] = useState('')
  const [sort, setSort] = useState('name')
  const [picks, setPicks] = useState<Record<string, string[]>>({})
  useEffect(() => { setQuery(''); setSort('name') }, [activeView])
  const matches = (item: { name: string; description?: string }) => `${item.name} ${item.description || ''}`.toLowerCase().includes(query.trim().toLowerCase())
  const connections = servers.filter(matches).sort((a, b) => sort === 'tools' ? (b.toolCount ?? 0) - (a.toolCount ?? 0) : a.name.localeCompare(b.name))
  const available = catalog.filter(matches).sort((a, b) => a.name.localeCompare(b.name))
  const groups = [...new Set(available.map(item => item.batch?.name || item.category || 'Connectors'))]
  const count = activeView === 'connected' ? connections.length : available.length
  return <div className="space-y-4 text-xs" data-testid="mcp-connections-panel">
    {(!view || headerActions) && <div className="flex min-w-0 items-center justify-between gap-2">
      {!view && <div className="min-w-0 flex-1 overflow-x-auto"><WorkspaceViewTabs value={tab} onChange={value => setTab(value as typeof tab)} ariaLabel="MCP browser"
        options={[{ value: 'connected', label: 'Connected', count: servers.length }, { value: 'available', label: 'Available', count: catalog.length }]} /></div>}
      {headerActions && <div className="shrink-0 whitespace-nowrap">{headerActions}</div>}
    </div>}
    <div className="flex flex-wrap items-center gap-2">
      <span className="relative min-w-40 flex-1 sm:max-w-xs"><Search className="pointer-events-none absolute left-2.5 top-1/2 h-3.5 w-3.5 -translate-y-1/2 text-muted-foreground" aria-hidden />
        <Input aria-label="Search servers" placeholder="Search servers…" value={query} onChange={event => setQuery(event.target.value)} className="pl-8" data-testid={searchTestId} /></span>
      <select aria-label="Sort servers" className="h-8 rounded-md border border-input bg-background px-2 text-xs" value={sort} onChange={event => setSort(event.target.value)}>
        <option value="name">Sort A–Z</option>{showTools && activeView === 'connected' && <option value="tools">Sort by tools</option>}
      </select>
      <span className="text-muted-foreground">{count} {count === 1 ? 'result' : 'results'}</span>
      {refresh && <Button size="icon" variant="ghost" className="h-7 w-7" aria-label="Refresh MCPs" disabled={loading} onClick={refresh}><RefreshCw className={loading ? 'animate-spin' : ''} /></Button>}
    </div>
    {notices.map((notice, i) => <div key={`${i}:${notice.message}`} role={notice.tone === 'info' ? undefined : 'alert'} className={notice.tone === 'info' ? 'text-muted-foreground' : 'text-destructive'}>
      {notice.message}{notice.retry && <Button variant="ghost" size="xs" onClick={notice.retry}>Retry</Button>}
    </div>)}
    {loading && <p className="flex items-center gap-1 text-muted-foreground"><Loader2 className="h-3 w-3 animate-spin" />Loading servers…</p>}
    {activeView === 'connected' ? <section aria-label="Connected servers" className="space-y-3">
      {!loading && connections.length === 0 && <p className="py-4 text-muted-foreground">No connected MCPs{query ? ' match your search' : ' yet'}.</p>}
      {connections.map(server => <McpConnectionCard key={server.id} server={server} showTools={showTools} />)}
    </section> : <>
      <section aria-label="Available servers" className="space-y-4">
        {!loading && available.length === 0 && <p className="py-4 text-muted-foreground">No servers available{query ? ' match your search' : ' to add'}.</p>}
        {groups.map(group => {
          const items = available.filter(item => (item.batch?.name || item.category || 'Connectors') === group)
          const batch = items[0].batch
          const selected = batch ? picks[batch.id] ?? [] : []
          return <section key={group} aria-label={group} className="space-y-2">
            <h4 className="font-medium text-muted-foreground">{GROUP_ORDER.find(item => item.id === group)?.label || group}</h4>
            <div className="space-y-2">{items.map(item => <div key={item.id} className="flex items-center gap-3 rounded-md border border-border p-3">
              {batch && <input type="checkbox" aria-label={`Add ${item.name}`} disabled={item.connect?.disabled} checked={selected.includes(item.id)} onChange={() => setPicks(value => ({ ...value, [batch.id]: selected.includes(item.id) ? selected.filter(id => id !== item.id) : [...selected, item.id] }))} />}
              <ConnectionIcon icon={brandSlugFor(item.name)} name={item.name} size="xs" />
              <div className="min-w-0 flex-1"><p className="truncate text-sm font-medium">{item.name}</p>{item.description && <p className="mt-1 truncate text-muted-foreground" title={item.description}>{item.description}</p>}</div>
              {!batch && (item.connectControl || (item.connect && <Button variant="outline" size="sm" disabled={item.connect.disabled} aria-label={item.connect.ariaLabel} data-testid={item.testId} onClick={() => void item.connect!.run()}>{item.connect.icon || <Plus />}{item.connect.label}</Button>))}
            </div>)}</div>
            {batch && selected.length > 0 && <Button size="sm" disabled={items.some(item => item.connect?.disabled)} onClick={() => { void Promise.resolve(batch.connect(selected)).then(result => { if (result !== false) setPicks(value => ({ ...value, [batch.id]: [] })) }) }}>Connect {selected.length} {selected.length === 1 ? 'service' : 'services'}</Button>}
          </section>
        })}
      </section>
      {addCustom && <div className="border-t border-border pt-4" aria-label="Add custom server"><Button variant="outline" size="sm" disabled={addCustom.disabled} onClick={() => void addCustom.run()} data-testid="mcp-add-custom"><Plus />{addCustom.label}</Button></div>}
    </>}
    {help && <details className="text-muted-foreground"><summary className="w-fit cursor-pointer">Connection help</summary><div className="mt-2 space-y-2">{help}</div></details>}
    {children}
  </div>
}

function McpConnectionCard({ server, showTools }: { server: McpConnectionRow; showTools: boolean }) {
  const [open, setOpen] = useState(false)
  const [tools, setTools] = useState<McpToolRow[] | null>(null)
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [attempt, setAttempt] = useState(0)
  const loadTools = useRef(server.loadTools)
  loadTools.current = server.loadTools
  useEffect(() => { setTools(null); setError(null) }, [server.id])
  useEffect(() => {
    if (!showTools || !open || !loadTools.current) return
    let cancelled = false
    setLoading(true); setError(null)
    void loadTools.current().then(result => { if (!cancelled) setTools(result) })
      .catch(cause => { if (!cancelled) setError(cause instanceof Error ? cause.message : 'Could not load tools.') })
      .finally(() => { if (!cancelled) setLoading(false) })
    return () => { cancelled = true }
  }, [showTools, open, server.id, attempt])
  const rows = server.tools ?? tools ?? []
  const actions = [...(server.actions ?? [])]
  if (showTools && server.loadTools) actions.unshift({ label: 'Refresh tool list', run: () => { setOpen(true); setAttempt(value => value + 1) } })
  const canExpand = showTools && (server.tools !== undefined || !!server.loadTools)
  return <article aria-label={`${server.name} server`} className="min-w-0 rounded-md border border-border p-3">
    <McpServerHeader name={server.name} status={server.status} statusDot={server.statusDot} detail={server.source && <span>· {server.source}</span>}
      toolCount={showTools ? server.toolCount ?? (tools ? tools.length : undefined) : undefined} expanded={open} toolsLabel={server.toolsLabel?.(open)} onToggleTools={canExpand ? () => setOpen(value => !value) : undefined}
      selection={server.selection && <input type="checkbox" aria-label={server.selection.label} checked={server.selection.checked} disabled={server.selection.disabled} onChange={() => void server.selection!.change()} />}
      actions={<>{server.controls}{actions.length > 0 && <IconPopover icon={<MoreHorizontal className="h-4 w-4" />} label={`Actions for ${server.name}`} panelClassName="w-52 !p-1">
        {close => <div role="group" aria-label="Server actions">{actions.map(action => <Button key={action.label} variant="ghost" size="sm" className={`w-full justify-start ${action.destructive ? 'text-destructive hover:text-destructive' : ''}`} aria-label={action.ariaLabel} disabled={action.disabled} onClick={() => { close(); void action.run() }}>{action.icon}{action.label}</Button>)}</div>}
      </IconPopover>}</>} />
    {server.settings}
    {showTools && open && <div className="mt-3 space-y-3 border-t border-border pt-3">
      {server.toolsNotice}
      {loading && <p className="flex items-center gap-1 text-muted-foreground"><Loader2 className="h-3 w-3 animate-spin" />Loading tools…</p>}
      {error && <div role="alert" className="text-destructive">{error}<Button size="xs" variant="ghost" onClick={() => setAttempt(value => value + 1)}>Retry</Button></div>}
      {!loading && !error && rows.map(tool => <McpToolCard key={tool.id} name={tool.name} description={tool.description} status={tool.status} schema={tool.schema} rawSchema={tool.rawSchema}>{tool.details}</McpToolCard>)}
      {!loading && !error && rows.length === 0 && <p className="text-muted-foreground">No tools discovered.</p>}
    </div>}
  </article>
}
