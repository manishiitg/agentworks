import { useCallback, useEffect, useRef, useState } from 'react'
import { RefreshCw } from 'lucide-react'
import { Button } from '../ui/Button'
import { knowledgebaseApi, knowledgebaseError, type BrainAccessMode, type KnowledgeProject } from '../../services/knowledgebaseApi'
import { WORKFLOW_KNOWLEDGE_SOURCES_REFRESH_EVENT } from './workflowEvents'
import { useAuthStore } from '../../stores/useAuthStore'
import { useWorkspaceConnectionStore } from '../../stores/useWorkspaceConnectionStore'
import { ProjectBrainLibrary } from './ProjectBrainLibrary'
import { WorkspacePanelActions } from '../workspace/WorkspacePanelHeaderSlots'
import { useProductSurfaceStore } from '../../stores/useProductSurfaceStore'
import { integrationProductAvailable } from '../integrations/integrationSections'

// The project's Brain access: Off, Read or Read & write. It never grants permission: the owner's and the output
// audience's folder roles apply. There are no folder bindings (PLAT-628); steps say in their descriptions which
// Brain folders they read and write.
type Props = { workspacePath: string; disabled?: boolean }
export function ProjectKnowledgebasePanel(props: Props) {
  const actor = useAuthStore(state => state.user?.id)
  const server = useWorkspaceConnectionStore(state => state.activeWorkspaceId)
  return <ProjectBrainContents key={`${server}:${actor}:${props.workspacePath}`} {...props} />
}
function ProjectBrainContents({ workspacePath, disabled = false }: Props) {
  const allowedProducts = useAuthStore(state => state.user?.allowed_products)
  const brainAvailable = integrationProductAvailable('knowledgebase', allowedProducts)
  const [project, setProject] = useState<KnowledgeProject | null>(null)
  const [loading, setLoading] = useState(true)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const generation = useRef(0)
  const refresh = useCallback(async () => {
    const version = ++generation.current
    setLoading(true); setProject(null); setError('')
    if (!brainAvailable) { setLoading(false); return }
    try {
      await knowledgebaseApi.bootstrap()
      const current = await knowledgebaseApi.project(workspacePath)
      if (version === generation.current) { setProject(current); setError('') }
    } catch (cause) {
      if (version === generation.current) {
        const status = (cause as { response?: { status?: number } }).response?.status
        setError(status === 404
          ? 'Brain could not be loaded for this project. Refresh to try again.'
          : knowledgebaseError(cause))
      }
    } finally { if (version === generation.current) setLoading(false) }
  }, [workspacePath, brainAvailable])
  useEffect(() => {
    void refresh()
    const changed = (event: Event) => {
      const detail = (event as CustomEvent<{ workspacePath?: string }>).detail
      if (!detail || detail.workspacePath === workspacePath) void refresh()
    }
    window.addEventListener('focus', changed)
    window.addEventListener(WORKFLOW_KNOWLEDGE_SOURCES_REFRESH_EVENT, changed)
    return () => { generation.current++; window.removeEventListener('focus', changed); window.removeEventListener(WORKFLOW_KNOWLEDGE_SOURCES_REFRESH_EVENT, changed) }
  }, [refresh, workspacePath])
  const mode: BrainAccessMode = project?.brain_access === 'off' || project?.brain_access === 'read' ? project.brain_access : 'write'
  const changeMode = async (next: BrainAccessMode) => {
    if (!project?.can_manage || disabled || loading || busy || next === mode) return
    const version = generation.current
    setBusy(true); setError('')
    try {
      await knowledgebaseApi.setProjectAccess({ workspace_path: workspacePath, mode: next, expected_manifest_version: project.manifest_version, request_id: crypto.randomUUID() })
      if (version === generation.current) window.dispatchEvent(new CustomEvent(WORKFLOW_KNOWLEDGE_SOURCES_REFRESH_EVENT, { detail: { workspacePath } }))
    } catch (cause) { if (version === generation.current) setError(knowledgebaseError(cause)) }
    finally { setBusy(false) }
  }
  const blocked = disabled || !project?.can_manage || busy || loading
  const options: Array<{ value: BrainAccessMode; label: string; hint: string }> = [
    { value: 'write', label: 'Read & write', hint: 'Agents can read and save knowledge within existing folder access.' },
    { value: 'read', label: 'Read only', hint: 'Agents can read permitted knowledge; they cannot change it.' },
    { value: 'off', label: 'Off', hint: 'Agents here cannot use Brain.' },
  ]
  if (!brainAvailable) return null
  return <div className="space-y-4 text-xs">
    <WorkspacePanelActions>
      <Button size="sm" variant="outline" onClick={() => useProductSurfaceStore.getState().setProductSurface('knowledgebase')}>Open Brain</Button>
      <Button variant="ghost" size="icon" className="h-7 w-7" aria-label="Refresh Brain" disabled={loading || busy} onClick={() => void refresh()}><RefreshCw className={loading ? 'animate-spin' : ''}/></Button>
    </WorkspacePanelActions>
    <section aria-label="Brain" className="space-y-3 rounded-lg border border-border p-4">
      <div className="flex items-center justify-between gap-2"><h3 className="text-sm font-semibold">Project access</h3>{project && <span className="rounded bg-muted px-2 py-1">{options.find(option => option.value === mode)?.label}</span>}</div>
      {error && <p role="alert" className="text-destructive">{error}</p>}
      {loading ? <p role="status" className="text-muted-foreground">Loading your access…</p> : project ? <>
        <div role="radiogroup" aria-label="Brain access" className="grid grid-cols-1 gap-2 sm:grid-cols-3">
          {options.map(option => <label key={option.value} title={option.hint} className={`flex cursor-pointer items-center gap-2 rounded-md border px-3 py-2 ${mode === option.value ? 'border-primary bg-primary/5' : 'border-border'} ${blocked ? 'cursor-not-allowed opacity-70' : ''}`}>
            <input type="radio" name={`brain-access-${workspacePath}`} value={option.value} checked={mode === option.value} disabled={blocked} onChange={() => void changeMode(option.value)}/>{option.label}
          </label>)}
        </div>
        <p className="text-muted-foreground">{options.find(option => option.value === mode)?.hint} The owner's and output audience's folder permissions still apply.</p>
        {!project.can_manage && <p className="text-muted-foreground">Only an owner of this project can change Brain access.</p>}
      </> : null}
    </section>
    {project && <ProjectBrainLibrary />}
  </div>
}
