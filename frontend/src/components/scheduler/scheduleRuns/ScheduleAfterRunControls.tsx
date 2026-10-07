import type { ScheduleAfterRun, ScheduledJob } from '../../../services/api-types'

const OPTIONS: { key: keyof ScheduleAfterRun; label: string; title: string }[] = [
  { key: 'backup', label: 'Backup', title: 'Save workflow state after each run when it changed' },
  { key: 'publish', label: 'Publish', title: 'Refresh the published report after each run when it changed' },
  { key: 'notify', label: 'Notify', title: 'Send the run summary: failures and changes go to your channels, routine runs are recorded in the dashboard' },
]

/** After-run options of a workflow schedule (PLAT-697 phase 0): plain checkboxes, no Pulse. */
export function ScheduleAfterRunControls({ job, disabled, onChange }: {
  job: ScheduledJob
  disabled?: boolean
  onChange: (job: ScheduledJob, next: ScheduleAfterRun) => void
}) {
  if (job.entity_type !== 'workflow' || job.schedule_type === 'webhook' || !job.after_run) return null
  const current = job.after_run
  return <span className="inline-flex flex-wrap items-center gap-x-2 gap-y-1" role="group" aria-label={`After each run of ${job.name}`}>
    <span>After run:</span>
    {OPTIONS.map(option => <label key={option.key} className="inline-flex items-center gap-1" title={option.title}>
      <input
        type="checkbox"
        className="h-3 w-3"
        checked={current[option.key]}
        disabled={disabled}
        onChange={event => onChange(job, { ...current, [option.key]: event.target.checked })}
      />
      {option.label}
    </label>)}
  </span>
}
