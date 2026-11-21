import { useCallback, useEffect, useState } from 'react'
import { ExternalLink, RefreshCw, Webhook, Eye } from 'lucide-react'
import { conditionTriggersApi } from '../../api/conditionTriggers'
import { useAuthStore } from '../../stores/useAuthStore'
import { useWorkspaceConnectionStore } from '../../stores/useWorkspaceConnectionStore'
import { workflowWebhooksApi } from '../../api/workflowWebhooks'
import { loadProductProjects } from '../../platform/chat/productProjects'
import { WORK_PROFILE_ID, WORK_PROJECTS_ROOT } from '../../products/work/workData'
import { workflowManifestApi } from '../../services/api'

export type TriggerOwner = { id: string; label: string; kind: 'workflow' | 'crew' }
type TriggerRow = { id: string; name: string; enabled: boolean; path: string; owner: TriggerOwner }

async function loadTriggers(kind: TriggerOwner['kind'], mode: 'webhooks' | 'watchers', current: () => boolean): Promise<{ rows: TriggerRow[]; failures: number }> {
  const owners = kind === 'crew'
    ? (await loadProductProjects(WORK_PROJECTS_ROOT, WORK_PROFILE_ID, { includeOwnSharedProjects: true })).map(project => ({
      id: project.id, label: project.identity?.name || project.title, kind,
    }))
    : (await workflowManifestApi.listWorkflowManifests()).workflows.map(workflow => ({
      id: workflow.manifest.id, label: workflow.manifest.label || workflow.workspace_path, kind,
      workspacePath: workflow.workspace_path,
    }))
  const rows: TriggerRow[] = []
  let failures = 0
  // Limit simultaneous requests when a user has many automations.
  for (let index = 0; index < owners.length; index += 8) {
    if (!current()) return { rows: [], failures: 0 }
    const batch = owners.slice(index, index + 8)
    const results = await Promise.allSettled(batch.map(async owner => {
      if (mode === 'watchers') {
        const result = await conditionTriggersApi.list(owner.id, kind === 'crew' ? WORK_PROFILE_ID : 'agentworks')
        return result.triggers.map(trigger => ({ id: trigger.id, name: trigger.name, enabled: trigger.enabled, path: trigger.cron_expression ? `Check: ${trigger.cron_expression}` : 'When something changes', owner }))
      }
      // A Crew has no webhooks of its own (#3286): its webhooks belong to its workflow, listed with the workflows.
      if (kind === 'crew') return []
      const triggers = (await workflowWebhooksApi.list('workspacePath' in owner ? owner.workspacePath : '')).triggers
      return triggers.filter(trigger => !trigger.kind).map(trigger => ({
        id: trigger.id, name: trigger.name, enabled: trigger.enabled, path: trigger.path, owner,
      }))
    }))
    results.forEach(result => {
      if (result.status === 'fulfilled') rows.push(...result.value)
      else failures += 1
    })
  }
  rows.sort((a, b) => a.owner.label.localeCompare(b.owner.label) || a.name.localeCompare(b.name))
  return { rows, failures }
}

export default function GlobalTriggersView({ kind, onOpen, mode = 'webhooks' }: {
  kind: TriggerOwner['kind']
  mode?: 'webhooks' | 'watchers'
  onOpen: (owner: TriggerOwner) => void
}) {
  const user = useAuthStore(state => state.user?.id)
  const server = useWorkspaceConnectionStore(state => state.activeWorkspaceId)
  const scope = `${server}:${user}:${kind}:${mode}`
  const [loadedScope, setLoadedScope] = useState('')
  const label = mode === 'watchers' ? 'Watchers' : 'Webhooks'
  const Icon = mode === 'watchers' ? Eye : Webhook
  const [search, setSearch] = useState('')
  const [rows, setRows] = useState<TriggerRow[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const [failures, setFailures] = useState(0)
  const [refreshToken, setRefreshToken] = useState(0)
  const refresh = useCallback(() => setRefreshToken(value => value + 1), [])

  useEffect(() => {
    let cancelled = false
    setLoading(true)
    setError('')
    setFailures(0)
    void loadTriggers(kind, mode, () => !cancelled).then(result => {
      if (cancelled) return
      setLoadedScope(scope)
      setRows(result.rows)
      setFailures(result.failures)
    }).catch(() => {
      if (!cancelled) setError(`Could not load ${label.toLowerCase()}.`)
    }).finally(() => { if (!cancelled) setLoading(false) })
    return () => { cancelled = true }
  }, [kind, mode, scope, refreshToken])
  const visibleRows = loadedScope === scope ? rows.filter(row => `${row.name} ${row.owner.label}`.toLowerCase().includes(search.toLowerCase())) : []

  return <div className="mx-auto h-full max-w-5xl overflow-y-auto px-5 py-4">
    <div className="mb-4 flex flex-wrap items-center justify-between gap-3">
      <div>
        <h2 className="text-sm font-semibold text-foreground">{label}</h2>
        <p className="text-xs text-muted-foreground">{error ? 'Unable to load' : loading || loadedScope !== scope ? `Loading ${label.toLowerCase()}…` : `${rows.length} ${label.toLowerCase()}`}</p>
      </div>
      <button type="button" onClick={refresh} disabled={loading} className="inline-flex items-center gap-2 rounded-md border border-border px-3 py-1.5 text-xs text-foreground hover:bg-muted disabled:opacity-50">
        <RefreshCw className={`h-3.5 w-3.5 ${loading ? 'animate-spin' : ''}`} /> Refresh
      </button>
    </div>
    <input type="search" aria-label={`Search ${label.toLowerCase()}`} placeholder={`Search ${label.toLowerCase()}…`} value={search} onChange={event => setSearch(event.target.value)} className="mb-4 h-9 w-full max-w-sm rounded-lg border border-border bg-transparent px-3 text-sm" />
    {error && <p role="alert" className="mb-3 text-sm text-destructive">{error}</p>}
    {failures > 0 && <p role="status" className="mb-3 text-sm text-warning">Could not load {label.toLowerCase()} for {failures} {kind === 'crew' ? 'Crews' : 'Workflows'}.</p>}
    {!loading && !error && visibleRows.length === 0 && <div className="rounded-lg border border-dashed border-border p-8 text-center text-sm text-muted-foreground">
      <Icon className="mx-auto mb-2 h-5 w-5" /> No matching {label.toLowerCase()}.
    </div>}
    {visibleRows.length > 0 && <ul className="divide-y divide-border rounded-xl border border-border">{visibleRows.map(row => <li key={`${row.owner.id}:${row.id}`} className="flex min-w-0 items-center gap-3 px-4 py-3">
      <Icon aria-hidden="true" className="h-4 w-4 shrink-0 text-muted-foreground" />
      <div className="min-w-0 flex-1"><p className="truncate text-sm font-medium">{row.name}</p><p className="truncate text-xs text-muted-foreground">{row.owner.label}</p></div>
      <span className="text-xs text-muted-foreground">{row.enabled ? 'Active' : 'Paused'}</span>
      <button type="button" onClick={() => onOpen(row.owner)} aria-label={`Open ${row.name}`} className="grid h-8 w-8 shrink-0 place-items-center rounded-md text-muted-foreground hover:bg-muted"><ExternalLink aria-hidden="true" className="h-3.5 w-3.5" /></button>
    </li>)}</ul>}
  </div>
}
