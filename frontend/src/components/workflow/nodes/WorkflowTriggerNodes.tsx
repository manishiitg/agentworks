import { memo } from 'react'
import { Handle, Position, type NodeProps } from '@xyflow/react'
import { Mail, CalendarClock, GitBranch, RefreshCw, Settings, Webhook } from 'lucide-react'
import type { WorkflowTriggerNodeData } from '../hooks/usePlanToFlow'
import { describeCron } from '../../scheduler/scheduleRuns/cron'
import { formatLocalScheduleTime } from '../../scheduler/scheduleRuns/helpers'
import { WebhookEndpoint } from '../../scheduler/scheduleRuns/WebhookEndpoint'

export const WorkflowTriggerNode = memo(({ data }: NodeProps) => {
  const { job, routeSummary, active, onSelect, onSettings, relayMode } = data as WorkflowTriggerNodeData
  if (!job) return null
  const webhook = job.schedule_type === 'webhook'
  const gmail = job.kind === 'gmail'
  const Icon = gmail ? Mail : webhook ? Webhook : CalendarClock
  const cadence = gmail ? 'On incoming email' : webhook ? 'On request' : job.schedule_type === 'calendar' ? `${job.calendar_items?.length || 0} calendar dates` : describeCron(job.cron_expression)
  const accent = webhook ? 'border-sky-500/35 bg-sky-500/10 text-sky-600 dark:text-sky-300' : 'border-amber-500/35 bg-amber-500/10 text-amber-600 dark:text-amber-300'
  return <article className={`nodrag nopan flex h-[236px] w-[288px] flex-col overflow-hidden rounded-2xl border bg-gradient-to-br p-3 text-card-foreground shadow-md backdrop-blur-sm ${webhook ? 'from-sky-500/10 via-card to-card' : 'from-amber-500/10 via-card to-card'} ${active ? 'border-primary ring-2 ring-primary/30' : 'border-border/80'}`} aria-label={`${gmail ? 'Gmail' : webhook ? 'Webhook' : 'Schedule'}: ${job.name}`}>
    <div className="flex min-w-0 items-start gap-2.5">
      <span className={`flex h-9 w-9 shrink-0 items-center justify-center rounded-xl border ${accent}`}><Icon className="h-4 w-4" /></span>
      <div className="min-w-0 flex-1">
        <div className="flex items-center gap-2 text-[10px] font-medium uppercase tracking-wide text-muted-foreground"><span>{gmail ? 'Gmail event' : webhook ? 'Webhook event' : 'Scheduled event'}</span><span className={`ml-auto h-2 w-2 rounded-full ${job.enabled ? 'bg-emerald-500' : 'bg-muted-foreground/50'}`} aria-hidden="true" /><span className="normal-case tracking-normal">{job.enabled ? 'Active' : 'Paused'}</span></div>
        <h3 className="mt-0.5 truncate text-sm font-semibold" title={job.name}>{job.name}</h3>
      </div>
    </div>
    <div className="mt-3 rounded-xl border border-border/70 bg-background/55 px-2.5 py-2">
      <p className="truncate text-xs font-medium" title={cadence}>{cadence}</p>
      {!webhook && <p className="mt-0.5 truncate text-[10px] text-muted-foreground">{job.enabled ? `Next: ${formatLocalScheduleTime(job.next_run_at)}` : 'Schedule paused'} · {job.timezone || 'UTC'}</p>}
      {gmail && <p className="mt-1 break-all text-[10px] font-mono select-all">{job.gmail?.address}</p>}
      {webhook && !gmail && <div className="mt-1 max-h-10 overflow-auto"><WebhookEndpoint id={job.id} name={job.name} /></div>}
    </div>
    <div className="mt-2 flex min-w-0 items-start gap-2 px-1">
      <GitBranch className="mt-0.5 h-3.5 w-3.5 shrink-0 text-primary" />
      <div className="min-w-0"><p className="text-[10px] font-medium uppercase tracking-wide text-muted-foreground">Starts</p><p className="line-clamp-2 text-xs" title={routeSummary?.label}>{routeSummary?.label}</p></div>
    </div>
    {!relayMode && !!job.group_names?.length && <p className="mt-1 truncate px-1 text-[10px] text-muted-foreground" title={job.group_names.join(', ')}>Access: {job.group_names.join(', ')}</p>}
    <div className="mt-auto flex items-center gap-2 border-t border-border/70 pt-2">
      <button type="button" onClick={onSelect} disabled={!routeSummary?.canTrace} aria-pressed={!!active} className="rounded-lg px-2 py-1 text-xs font-medium text-primary hover:bg-primary/10 disabled:text-muted-foreground disabled:opacity-50">{active ? 'Clear path' : 'View path'}</button>
      <button type="button" onClick={() => onSettings?.(webhook ? 'webhooks' : 'schedules')} className="ml-auto rounded-lg p-1.5 text-muted-foreground hover:bg-muted hover:text-foreground" aria-label={`Open settings for ${job.name}`} title={`Open ${webhook ? 'webhooks' : 'schedules'}`}><Settings className="h-4 w-4" /></button>
    </div>
    {routeSummary?.canTrace && <Handle type="source" position={Position.Bottom} className="!h-2 !w-2 !bg-muted-foreground" />}
  </article>
})
WorkflowTriggerNode.displayName = 'WorkflowTriggerNode'

export const WorkflowTriggerHeading = memo(({ data }: NodeProps) => {
  const { loading, error, count, onSettings, onRefresh } = data as WorkflowTriggerNodeData
  return <div className="nodrag nopan h-16 space-y-1 text-foreground">
    <div className="flex items-center gap-2 text-sm font-semibold"><button type="button" onClick={onRefresh} aria-label="Refresh triggers" className="rounded p-1 hover:bg-muted"><RefreshCw className={`h-3 w-3 ${loading ? 'animate-spin' : ''}`} /></button></div>
    <p className="text-xs text-muted-foreground">{error || (loading ? 'Loading schedules and webhooks…' : count ? 'Solid lines start the workflow; dashed lines select routes.' : 'No automatic triggers. This workflow can be started manually.')}</p>
    <div className="flex gap-3 text-xs"><button type="button" className="hover:underline" onClick={() => onSettings?.('schedules')}>Schedules</button><button type="button" className="hover:underline" onClick={() => onSettings?.('webhooks')}>Webhooks</button></div>
  </div>
})
WorkflowTriggerHeading.displayName = 'WorkflowTriggerHeading'
