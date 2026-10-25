import { useEffect, useState } from 'react'
import { workflowManifestApi } from '../../services/api'
import { useWorkflowManifestStore } from '../../stores/useWorkflowManifestStore'

/** The server owns the selection and checks the same Goal write access as chat. */
export function GoalAfterRunToggle({ workspacePath, option }: { workspacePath: string | null; option: 'backup' | 'publish' }) {
  const [value, setValue] = useState<boolean | null>(null)
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState('')
  const access = useWorkflowManifestStore(state => state.workflows.find(item => item.workspace_path === workspacePath)?.my_access)
  const canEdit = access === 'owner' || access === 'write'
  useEffect(() => {
    let cancelled = false
    setValue(null)
    setError('')
    if (workspacePath) {
      workflowManifestApi.getWorkflowManifest(workspacePath).then(response => {
        if (!cancelled && response.manifest?.kind !== 'relay') setValue(Boolean(response.manifest?.[option]?.after_runs))
      }).catch(() => { if (!cancelled) setError('Could not load this setting. Refresh the panel to try again.') })
    }
    return () => { cancelled = true }
  }, [workspacePath, option])
  const save = async () => {
    if (!workspacePath || value === null) return
    setSaving(true)
    setError('')
    try {
      const response = await workflowManifestApi.updateWorkflowManifest({ workspace_path: workspacePath, [option]: { after_runs: !value } })
      setValue(Boolean(response.manifest?.[option]?.after_runs))
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Could not save. Ask the Goal owner to update this setting.')
    } finally { setSaving(false) }
  }
  return <div className="text-xs">
    {value !== null && <button type="button" role="switch" aria-checked={value} disabled={!canEdit || saving} onClick={() => void save()}
      className="inline-flex items-center gap-2 rounded-md px-2 py-1 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring disabled:opacity-50"
      title="Uses the configured destination after every full run. Unchanged files are skipped.">
      <span>After every run</span>
      <span aria-hidden="true" className={`inline-flex h-5 w-9 items-center rounded-full p-0.5 ${value ? 'bg-primary' : 'bg-muted-foreground/25'}`}><span className={`h-4 w-4 rounded-full bg-white shadow-sm ${value ? 'translate-x-4' : ''}`} /></span>
    </button>}
    {error && <p role="alert" className="max-w-xs text-destructive">{error}</p>}
  </div>
}
