import { useCallback, useEffect, useState } from 'react'
import { ExternalLink, RefreshCw, Webhook } from 'lucide-react'
import { productWebhooksApi } from '../../api/productWebhooks'
import { workflowWebhooksApi } from '../../api/workflowWebhooks'
import { loadProductProjects } from '../../platform/chat/productProjects'
import { WORK_PROFILE_ID, WORK_PROJECTS_ROOT } from '../../products/work/workData'
import { workflowManifestApi } from '../../services/api'

export type TriggerOwner = { id: string; label: string; kind: 'workflow' | 'crew' }
type TriggerRow = { id: string; name: string; enabled: boolean; path: string; owner: TriggerOwner }

async function loadTriggers(kind: TriggerOwner['kind']): Promise<{ rows: TriggerRow[]; failures: number }> {
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
    const batch = owners.slice(index, index + 8)
    const results = await Promise.allSettled(batch.map(async owner => {
      const triggers = kind === 'crew'
        ? (await productWebhooksApi.list({ profileId: WORK_PROFILE_ID, projectId: owner.id })).triggers
        : (await workflowWebhooksApi.list('workspacePath' in owner ? owner.workspacePath : '')).triggers
      return triggers.filter(trigger => !trigger.kind || trigger.kind === 'gmail').map(trigger => ({
        id: trigger.id, name: trigger.name, enabled: trigger.enabled, path: trigger.kind === 'gmail' ? trigger.gmail?.address || '' : trigger.path, owner,
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

export default function GlobalTriggersView({ kind, onOpen }: {
  kind: TriggerOwner['kind']
  onOpen: (owner: TriggerOwner) => void
}) {
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
    void loadTriggers(kind).then(result => {
      if (cancelled) return
      setRows(result.rows)
      setFailures(result.failures)
    }).catch(() => {
      if (!cancelled) setError(`Could not load ${kind === 'crew' ? 'Crew' : 'Workflow'} triggers.`)
    }).finally(() => { if (!cancelled) setLoading(false) })
    return () => { cancelled = true }
  }, [kind, refreshToken])

  return <div className="h-full overflow-y-auto px-4 py-5 sm:px-6">
    <div className="mb-4 flex flex-wrap items-center justify-between gap-3">
      <div>
        <h2 className="text-sm font-semibold text-foreground">{kind === 'crew' ? 'Crew' : 'Workflow'} triggers</h2>
        <p className="text-xs text-muted-foreground">{loading ? 'Loading triggers…' : `${rows.length} trigger${rows.length === 1 ? '' : 's'}`}</p>
      </div>
      <button type="button" onClick={refresh} disabled={loading} className="inline-flex items-center gap-2 rounded-md border border-border px-3 py-1.5 text-xs text-foreground hover:bg-muted disabled:opacity-50">
        <RefreshCw className={`h-3.5 w-3.5 ${loading ? 'animate-spin' : ''}`} /> Refresh
      </button>
    </div>
    {error && <p role="alert" className="mb-3 text-sm text-destructive">{error}</p>}
    {failures > 0 && <p role="status" className="mb-3 text-sm text-warning">Could not load triggers for {failures} {kind === 'crew' ? 'Crews' : 'Workflows'}.</p>}
    {!loading && !error && rows.length === 0 && <div className="rounded-lg border border-dashed border-border p-8 text-center text-sm text-muted-foreground">
      <Webhook className="mx-auto mb-2 h-5 w-5" /> No {kind === 'crew' ? 'Crew' : 'Workflow'} triggers yet.
    </div>}
    {rows.length > 0 && <div className="overflow-x-auto rounded-lg border border-border">
      <table className="w-full min-w-[620px] text-left text-sm">
        <thead className="border-b border-border bg-muted/30 text-xs text-muted-foreground"><tr>
          <th className="px-4 py-2 font-medium">{kind === 'crew' ? 'Crew' : 'Workflow'}</th>
          <th className="px-4 py-2 font-medium">Trigger</th>
          <th className="px-4 py-2 font-medium">Address / endpoint</th>
          <th className="px-4 py-2 font-medium">Status</th>
          <th className="px-4 py-2 font-medium">Action</th>
        </tr></thead>
        <tbody className="divide-y divide-border">{rows.map(row => <tr key={`${row.owner.id}:${row.id}`}>
          <td className="px-4 py-3 font-medium text-foreground">{row.owner.label}</td>
          <td className="px-4 py-3 text-foreground">{row.name}</td>
          <td className="max-w-[300px] truncate px-4 py-3 font-mono text-xs text-muted-foreground" title={row.path}>{row.path}</td>
          <td className="px-4 py-3 text-xs">{row.enabled ? 'Active' : 'Paused'}</td>
          <td className="px-4 py-3"><button type="button" onClick={() => onOpen(row.owner)} className="inline-flex items-center gap-1 text-xs font-medium text-primary hover:underline">Open <ExternalLink className="h-3 w-3" /></button></td>
        </tr>)}</tbody>
      </table>
    </div>}
  </div>
}
