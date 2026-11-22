import { useCallback, useEffect, useState } from 'react'
import { Webhook, Eye } from 'lucide-react'
import { DetailLine, PageRow } from '../automation/AutomationKit'
import { WorkspacePageContent } from '../workspace/WorkspacePageContent'
import { AutomationOverviewToolbar } from './AutomationOverviewToolbar'
import { describeCron } from './scheduleRuns/cron'
import { conditionTriggersApi } from '../../api/conditionTriggers'
import { useAuthStore } from '../../stores/useAuthStore'
import { useWorkspaceConnectionStore } from '../../stores/useWorkspaceConnectionStore'
import { workflowWebhooksApi } from '../../api/workflowWebhooks'
import { loadProductProjects } from '../../platform/chat/productProjects'
import { WORK_PROFILE_ID, WORK_PROJECTS_ROOT } from '../../products/work/workData'
import { workflowManifestApi } from '../../services/api'

export type TriggerOwner = { id: string; label: string; kind: 'workflow' | 'crew' }
type TriggerRow = { id: string; name: string; enabled: boolean; timing: string; owner: TriggerOwner }

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
        return result.triggers.map(trigger => ({ id: trigger.id, name: trigger.name, enabled: trigger.enabled, timing: trigger.cron_expression ? describeCron(trigger.cron_expression) : 'When something changes', owner }))
      }
      // A Crew has no webhooks of its own (#3286): its webhooks belong to its workflow, listed with the workflows.
      if (kind === 'crew') return []
      const triggers = (await workflowWebhooksApi.list('workspacePath' in owner ? owner.workspacePath : '')).triggers
      return triggers.filter(trigger => !trigger.kind).map(trigger => ({
        id: trigger.id, name: trigger.name, enabled: trigger.enabled, timing: 'Starts the workflow when a request arrives.', owner,
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
  const label = mode === 'watchers' ? 'watchers' : 'webhooks'
  const Icon = mode === 'watchers' ? Eye : Webhook
  const [search, setSearch] = useState('')
  const [openRow, setOpenRow] = useState<string | null>(null)
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
      if (!cancelled) setError(`Could not load ${label}.`)
    }).finally(() => { if (!cancelled) setLoading(false) })
    return () => { cancelled = true }
  }, [kind, mode, scope, refreshToken])
  const query = search.trim().toLowerCase()
  const currentRows = loadedScope === scope ? rows : []
  const visibleRows = currentRows.filter(row => `${row.name} ${row.owner.label}`.toLowerCase().includes(query))

  return <WorkspacePageContent className="flex h-full min-h-0 w-full flex-col">
    <AutomationOverviewToolbar label={label} search={search} onSearch={setSearch} loading={loading} onRefresh={refresh} />
    <div className="min-h-0 flex-1 overflow-y-auto">
      {error && <p role="alert" className="mb-3 rounded-lg border border-border bg-card p-4 text-sm">{error} <button type="button" className="underline" onClick={refresh}>Retry</button></p>}
      {failures > 0 && <p role="status" className="mb-3 text-sm text-warning">Could not load {label} for {failures} {kind === 'crew' ? 'Crews' : 'workflows'}.</p>}
      {loading && currentRows.length === 0 && <p role="status" className="rounded-lg border border-border bg-card p-4 text-sm text-muted-foreground">Loading {label}…</p>}
      {!loading && !error && visibleRows.length === 0 && <p className="rounded-lg border border-border bg-card p-4 text-sm text-muted-foreground">
        {query && currentRows.length > 0
          ? <>No matching {label}. <button type="button" className="underline" onClick={() => setSearch('')}>Clear search</button></>
          : failures === 0 ? <>No {label} yet. {mode === 'watchers' ? 'Ask an agent in a Crew or workflow to set one up.' : 'Open a workflow to add a webhook.'}</> : <>No {label} to show.</>}
      </p>}
      {!error && visibleRows.length > 0 && <div className="divide-y divide-border overflow-hidden rounded-lg border border-border bg-card">{visibleRows.map(row => {
        const key = `${row.owner.id}:${row.id}`
        return <PageRow key={key} lead={<Icon aria-hidden="true" className="h-4 w-4 text-muted-foreground" />}
          title={<span title={row.name}>{row.name}</span>} lines={[row.owner.label]} aside={row.enabled ? 'Active' : 'Paused'}
          open={openRow === key} onToggle={() => setOpenRow(openRow === key ? null : key)}>
          <DetailLine label="When">{row.timing}</DetailLine>
          <button type="button" onClick={() => onOpen(row.owner)} className="text-primary hover:underline">Open {row.owner.label}</button>
        </PageRow>
      })}</div>}
    </div>
  </WorkspacePageContent>
}
