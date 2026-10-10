import type { ScheduleAfterRun, ScheduledJob } from '../../../services/api-types'
import { AfterRunActions } from './AfterRunActions'

/** Keep after-run settings compact while the schedule timing stays prominent. */
export function ScheduleAfterRunControls({ job, disabled, onChange }: {
  job: ScheduledJob
  disabled?: boolean
  onChange: (job: ScheduledJob, next: ScheduleAfterRun) => void | Promise<void>
}) {
  if (job.entity_type !== 'workflow' || job.schedule_type === 'webhook' || !job.after_run) return null
  return <AfterRunActions options={job.after_run} scope={job.name} disabled={disabled} onChange={next => onChange(job, next)} />
}
