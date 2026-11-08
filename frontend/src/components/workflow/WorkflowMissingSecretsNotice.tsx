import { useEffect, useState } from 'react'
import { workflowManifestApi } from '../../services/api'

// This fetch remains independent of the retired contract-upgrade status. Never
// show one workflow's selected secret names while another workflow is loading.
export function useWorkflowMissingSecrets(workspacePath: string | null) {
  const [status, setStatus] = useState<{ workspacePath: string; names: string[] } | null>(null)
  useEffect(() => {
    let cancelled = false
    if (!workspacePath) {
      setStatus(null)
      return
    }
    void workflowManifestApi.getWorkflowManifest(workspacePath).then(response => {
      if (!cancelled) setStatus({ workspacePath, names: response.missing_secrets ?? [] })
    }).catch(() => {
      if (!cancelled) setStatus(null)
    })
    return () => { cancelled = true }
  }, [workspacePath])
  return status?.workspacePath === workspacePath ? status.names : []
}

export default function WorkflowMissingSecretsNotice({ names }: { names: string[] }) {
  if (names.length === 0) return null
  return (
    <div
      role="status"
      data-testid="missing-secrets-banner"
      className="border-b border-amber-200 bg-amber-50 px-4 py-2 text-sm text-amber-900 dark:border-amber-800/50 dark:bg-amber-900/20 dark:text-amber-200"
    >
      {names.length === 1 ? 'This workflow uses a secret that has no value: ' : 'This workflow uses secrets that have no value: '}
      <span className="font-medium">{names.join(', ')}</span>.
      {' '}Runs still start, but anything that needs {names.length === 1 ? 'it' : 'them'} will get an empty value.
      {' '}Add the value in Connections → Add connection → Secrets, or remove the selection.
    </div>
  )
}
