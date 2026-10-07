import { useEffect, useState } from 'react'
import { workflowManifestApi } from '../../../services/api'
import type { ScheduleAfterRun, WorkflowManifest } from '../../../services/api-types'

const LABELS: { key: keyof ScheduleAfterRun; label: string }[] = [
  { key: 'backup', label: 'Backup' },
  { key: 'publish', label: 'Publish' },
  { key: 'notify', label: 'Notify' },
]

function manualAfterRun(manifest: WorkflowManifest): ScheduleAfterRun {
  if (manifest.after_manual_run) return manifest.after_manual_run
  return {
    backup: !!(manifest.backup?.enabled && manifest.backup.triggers?.after_manual_run),
    publish: !!(manifest.publish?.enabled && manifest.publish.triggers?.after_manual_run),
    notify: false,
  }
}

/** The same after-run choices as a schedule, for full runs started from a chat (PLAT-697 phase 0). */
export function ManualRunAfterRunControls({ workspacePath, disabled }: { workspacePath: string; disabled?: boolean }) {
  const [options, setOptions] = useState<ScheduleAfterRun | null>(null)
  const [saving, setSaving] = useState(false)
  useEffect(() => {
    let cancelled = false
    workflowManifestApi.getWorkflowManifest(workspacePath)
      .then(response => { if (!cancelled && response?.manifest && response.manifest.kind !== 'relay') setOptions(manualAfterRun(response.manifest)) })
      .catch(() => { /* the schedules still show without this row */ })
    return () => { cancelled = true }
  }, [workspacePath])
  if (!options) return null
  const save = async (next: ScheduleAfterRun) => {
    const previous = options
    setOptions(next)
    setSaving(true)
    try {
      await workflowManifestApi.updateWorkflowManifest({ workspace_path: workspacePath, after_manual_run: next })
    } catch {
      setOptions(previous)
    } finally {
      setSaving(false)
    }
  }
  return <div className="flex flex-wrap items-center gap-x-2 gap-y-1 px-4 pt-3 text-xs text-muted-foreground sm:px-6" role="group" aria-label="After each manual run">
    <span>After a manual run:</span>
    {LABELS.map(option => <label key={option.key} className="inline-flex items-center gap-1">
      <input
        type="checkbox"
        className="h-3 w-3"
        checked={options[option.key]}
        disabled={disabled || saving}
        onChange={event => void save({ ...options, [option.key]: event.target.checked })}
      />
      {option.label}
    </label>)}
  </div>
}
