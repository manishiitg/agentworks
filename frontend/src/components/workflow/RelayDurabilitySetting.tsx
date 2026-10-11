import { useEffect, useState } from 'react'
import { agentApi } from '../../services/api'
import { useWorkflowManifestStore } from '../../stores/useWorkflowManifestStore'
import { useCanWriteWorkflow } from '../../hooks/useCanWriteWorkflow'
import { useRelaySourceGraph } from './useRelaySourceGraph'
import { SettingsCard } from '../ui/SettingsCard'

export function RelayDurabilitySetting({ workspacePath }: { workspacePath: string | null }) {
  const canWrite = useCanWriteWorkflow(workspacePath)
  const [source, setSource] = useState<string | null>(null)
  const [relayID, setRelayID] = useState<string | null>(null)
  const overview = useRelaySourceGraph(relayID, source)
  const [enabled, setEnabled] = useState(false)
  const [loading, setLoading] = useState(true)
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState('')
  useEffect(() => {
    let active = true
    setLoading(true); setError(''); setSource(null); setRelayID(null)
    if (!workspacePath) { setLoading(false); return }
    void Promise.all([agentApi.getWorkflowManifest(workspacePath), agentApi.getPlannerFileContent(`${workspacePath}/relay.py`)]).then(([response, file]) => {
      if (typeof file?.data?.content !== 'string' || !file.data.content.trim()) throw new Error('Relay source unavailable')
      if (active) { setEnabled(response.manifest.relay_durability === 'dbos'); setRelayID(response.manifest.id || null); setSource(file.data.content); setLoading(false) }
    }).catch(() => { if (active) { setError('Could not load recovery settings.'); setLoading(false) } })
    return () => { active = false }
  }, [workspacePath])
  async function save(next: boolean) {
    if (!workspacePath || !canWrite || !source || !relayID || loading || saving || error || overview.loading || overview.error || overview.native) return
    setSaving(true); setError('')
    try {
      await agentApi.updateWorkflowManifest({ workspace_path: workspacePath, relay_durability: next ? 'dbos' : '' })
      setEnabled(next)
      await useWorkflowManifestStore.getState().refreshWorkflows()
    } catch { setError('Could not save recovery settings.') }
    finally { setSaving(false) }
  }
  return <SettingsCard title="Crash recovery" description="Continue an interrupted invocation from its completed steps.">
    <label className="flex items-center gap-2 text-sm"><input type="checkbox" checked={enabled} disabled={!canWrite || !source || !relayID || loading || saving || !!error || overview.loading || !!overview.error || overview.native} onChange={event => void save(event.target.checked)} />Enable DBOS recovery</label>
    <p className="text-xs text-muted-foreground">{overview.native ? 'Native DBOS programs require recovery to stay enabled. Service writes need idempotency; uncertain agent and MCP calls stop for review.' : 'Ask the Builder to prepare service actions for safe recovery. Uncertain actions stop for review.'} Publish a new version to apply this setting to API calls.</p>
    {error && <p role="alert" className="text-xs text-destructive">{error}</p>}
    {overview.error && <p role="alert" className="text-xs text-destructive">Could not inspect Relay source: {overview.error}</p>}
  </SettingsCard>
}
