import { useCallback, useEffect, useMemo, useState } from 'react'
import { AlertCircle, CheckCircle2, ChevronDown, ChevronUp, Clock3, Loader2, PackageCheck } from 'lucide-react'
import { workflowManifestApi } from '../../services/api'
import type { WorkflowContractUpgradeItem, WorkflowContractUpgradeStatus } from '../../services/api-types'
import { hasWorkflowContractUpgrade, WORKFLOW_CONTRACT_UPGRADE_STATUS_EVENT, type WorkflowContractUpgradeStatusEvent } from './workflowContractUpgradeEvents'

function formatUpgradeLabel(label: string): string {
  return label
    .replace(/^upgrade-/, '')
    .split('-')
    .map(word => word.charAt(0).toUpperCase() + word.slice(1))
    .join(' ')
}

function UpgradeRow({ item, state }: { item: WorkflowContractUpgradeItem; state: 'pending' | 'applied' }) {
  const [expanded, setExpanded] = useState(false)
  const pending = state === 'pending'
  const Icon = pending ? Clock3 : CheckCircle2
  return (
    <li>
      <div className="flex items-start gap-3 px-4 py-3">
        <span className={`mt-0.5 flex h-6 w-6 shrink-0 items-center justify-center rounded-full ${pending ? 'bg-amber-500/10 text-amber-600 dark:text-amber-300' : 'bg-emerald-500/10 text-emerald-600 dark:text-emerald-300'}`}>
          <Icon className="h-3.5 w-3.5" />
        </span>
        <div className="min-w-0 flex-1">
          <p className="text-sm font-medium text-foreground">{formatUpgradeLabel(item.label)}</p>
          <p className="mt-0.5 text-xs text-muted-foreground">
            Contract {item.target_version}
          </p>
        </div>
        <span className={`rounded-full border px-2 py-0.5 text-[11px] font-medium ${pending ? 'border-amber-500/30 bg-amber-500/10 text-amber-700 dark:text-amber-300' : 'border-emerald-500/30 bg-emerald-500/10 text-emerald-700 dark:text-emerald-300'}`}>
          {pending ? 'Pending' : 'Applied'}
        </span>
        <button
          type="button"
          onClick={() => setExpanded(value => !value)}
          aria-expanded={expanded}
          aria-label={`${expanded ? 'Hide' : 'Show'} details for ${formatUpgradeLabel(item.label)}`}
          className="flex h-6 w-6 shrink-0 items-center justify-center rounded-md border border-border text-muted-foreground transition-colors hover:border-primary/40 hover:text-primary"
        >
          {expanded ? <ChevronUp className="h-3.5 w-3.5" /> : <ChevronDown className="h-3.5 w-3.5" />}
        </button>
      </div>
      {expanded && (
        <div className="border-t border-border bg-muted/30 py-3 pl-[3.25rem] pr-4">
          <p className="text-xs font-semibold text-foreground">What changed</p>
          <p className="mt-1 text-xs leading-5 text-muted-foreground">{item.details || 'No migration details are available.'}</p>
        </div>
      )}
    </li>
  )
}

export default function WorkflowUpdatesView({ workspacePath }: { workspacePath: string | null }) {
  const [status, setStatus] = useState<WorkflowContractUpgradeStatus | null>(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)

  const load = useCallback(async () => {
    if (!workspacePath) return
    setLoading(true)
    setError(null)
    try {
      const response = await workflowManifestApi.getWorkflowManifest(workspacePath)
      setStatus(response.contract_upgrade ?? null)
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : 'Could not load workflow updates')
    } finally {
      setLoading(false)
    }
  }, [workspacePath])

  useEffect(() => { void load() }, [load])
  useEffect(() => {
    const onStatus = (event: Event) => {
      const detail = (event as WorkflowContractUpgradeStatusEvent).detail
      if (detail?.workspacePath !== workspacePath) return
      setStatus(detail.status)
      setError(null)
      setLoading(false)
    }
    window.addEventListener(WORKFLOW_CONTRACT_UPGRADE_STATUS_EVENT, onStatus)
    return () => window.removeEventListener(WORKFLOW_CONTRACT_UPGRADE_STATUS_EVENT, onStatus)
  }, [workspacePath])

  const pending = status?.pending ?? []
  const needsUpdate = hasWorkflowContractUpgrade(status)
  const applied = useMemo(() => [...(status?.applied ?? [])].reverse(), [status?.applied])
  const currentVersion = status?.current_version || '—'
  const platformVersion = status?.platform_version || '—'

  return (
    <div className="space-y-5">
      {error && (
        <div className="flex items-center gap-2 rounded-md bg-destructive/10 px-3 py-2 text-xs text-destructive">
          <AlertCircle className="h-3.5 w-3.5 shrink-0" />{error}
        </div>
      )}

      {loading && !status ? (
        <div className="flex items-center justify-center py-12"><Loader2 className="h-5 w-5 animate-spin text-muted-foreground" /></div>
      ) : status ? (
        <>
            <section className="flex flex-wrap items-center gap-3 rounded-md border border-border bg-muted/40 p-3">
              <span className={`flex h-9 w-9 shrink-0 items-center justify-center rounded-md ${needsUpdate ? 'bg-amber-500/10 text-amber-600 dark:text-amber-300' : 'bg-emerald-500/10 text-emerald-600 dark:text-emerald-300'}`}>
                <PackageCheck className="h-4 w-4" />
              </span>
              <div className="min-w-0 flex-1">
                <div className="flex flex-wrap items-center gap-2">
                  <h3 className="text-sm font-semibold text-foreground">Workflow contract</h3>
                  <span className={`rounded-full border px-2 py-0.5 text-xs font-medium ${needsUpdate ? 'border-amber-500/30 bg-amber-500/10 text-amber-700 dark:text-amber-300' : 'border-emerald-500/30 bg-emerald-500/10 text-emerald-700 dark:text-emerald-300'}`}>
                    {needsUpdate ? (pending.length > 0 ? `${pending.length} pending` : 'Needs attention') : 'Up to date'}
                  </span>
                </div>
                <p className="mt-0.5 text-xs text-muted-foreground">Current v{currentVersion} · platform v{platformVersion}</p>
              </div>
            </section>

            <section>
              <div className="mb-2 flex items-baseline justify-between gap-3">
                <div>
                  <h3 className="text-sm font-semibold text-foreground">Pending</h3>
                  <p className="mt-0.5 text-xs text-muted-foreground">Started manually in chat. Schedules keep running the saved version and never apply updates.</p>
                </div>
                <span className="text-xs tabular-nums text-muted-foreground">{pending.length}</span>
              </div>
              <ul className="divide-y divide-border overflow-hidden rounded-md border border-border">
                {pending.length > 0
                  ? pending.map(item => <UpgradeRow key={`${item.label}:${item.target_version}`} item={item} state="pending" />)
                  : <li className="px-4 py-6 text-center text-sm text-muted-foreground">
                    {status.required ? 'This contract needs review, but this server has no known upgrade path.' : 'No pending updates.'}
                  </li>}
              </ul>
            </section>

            <section>
              <div className="mb-2 flex items-baseline justify-between gap-3">
                <div>
                  <h3 className="text-sm font-semibold text-foreground">Applied before</h3>
                  <p className="mt-0.5 text-xs text-muted-foreground">Confirmed by the workflow's current contract marker; older updates did not record timestamps.</p>
                </div>
                <span className="text-xs tabular-nums text-muted-foreground">{applied.length}</span>
              </div>
              <ul className="divide-y divide-border overflow-hidden rounded-md border border-border">
                {applied.length > 0
                  ? applied.map(item => <UpgradeRow key={`${item.label}:${item.target_version}`} item={item} state="applied" />)
                  : <li className="px-4 py-6 text-center text-sm text-muted-foreground">No earlier platform updates are recorded by this contract.</li>}
              </ul>
            </section>
        </>
      ) : null}
    </div>
  )
}
