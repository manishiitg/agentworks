import { useCallback, useEffect, useRef, useState } from 'react'
import { Brain, RefreshCw } from 'lucide-react'
import { Button } from '../ui/Button'
import { knowledgebaseApi, knowledgebaseError, type BrainAccessMode, type KnowledgeProject } from '../../services/knowledgebaseApi'
import { AskAIButton } from './AskAIButton'
import { WORKFLOW_KNOWLEDGE_SOURCES_REFRESH_EVENT } from './workflowEvents'

// The project's Brain access: Off, Read or Read & write. It never grants permission: the owner's and the output
// audience's folder roles apply. There are no folder bindings (PLAT-628); steps say in their descriptions which
// Brain folders they read and write.
export function ProjectKnowledgebasePanel({ workspacePath, disabled = false }: { workspacePath: string; disabled?: boolean }) {
  const [project, setProject] = useState<KnowledgeProject | null>(null)
  const [loading, setLoading] = useState(true)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [available, setAvailable] = useState(true)
  const generation = useRef(0)
  const refresh = useCallback(async () => {
    const version = ++generation.current
    setLoading(true)
    try {
      await knowledgebaseApi.bootstrap()
      const current = await knowledgebaseApi.project(workspacePath)
      if (version === generation.current) { setProject(current); setAvailable(true); setError('') }
    } catch (cause) {
      if (version === generation.current) {
        if ((cause as { response?: { status?: number } }).response?.status === 404) setAvailable(false)
        else setError(knowledgebaseError(cause))
      }
    } finally { if (version === generation.current) setLoading(false) }
  }, [workspacePath])
  useEffect(() => {
    setProject(null); setError(''); setAvailable(true)
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
  if (!available) return null
  const blocked = disabled || !project?.can_manage || busy || loading
  const options: Array<{ value: BrainAccessMode; label: string; hint: string }> = [
    { value: 'write', label: 'Read & write', hint: 'The default. Agents read and write wherever you may. Each step’s description says which folders it reads and writes.' },
    { value: 'read', label: 'Read only', hint: 'Read all of Brain that you and everyone who sees this output can read. Never writes.' },
    { value: 'off', label: 'Off', hint: 'Agents here cannot use Brain.' },
  ]
  const noun = 'project'
  return <section aria-label="Brain" className="space-y-3 rounded-lg border border-border p-4 text-xs">
    <div className="flex flex-wrap items-center justify-between gap-2">
      <h3 className="flex items-center gap-2 text-sm font-semibold"><Brain className="h-4 w-4 text-primary"/>Brain</h3>
      <div className="flex items-center gap-1"><AskAIButton workspacePath={disabled ? null : workspacePath} label="Ask AI to set up" message={`Set up Brain for this ${noun}. Brain is read & write by default; ask only if I want it read-only or off. Use inspect_project for the current manifest version, then set_project_access for the mode. Then make sure each step that uses Brain says so in its description: the notes it reads under Inputs or Guides (brain:<folder>/<note>, real paths from brain_browse), what it writes under Output, and any limit under Rules. Steps that must not write get knowledgebase_access=read. Do not change folder grants.`}/><Button variant="ghost" size="icon" className="h-7 w-7" aria-label="Refresh Brain" disabled={loading || busy} onClick={() => void refresh()}><RefreshCw className={loading ? 'animate-spin' : ''}/></Button></div>
    </div>
    <p className="text-muted-foreground">Shared knowledge for this {noun}'s agents. This does not grant permission: your folder roles and those of everyone who sees the output still apply.</p>
    {error && <p role="alert" className="text-destructive">{error}</p>}
    {loading ? <p role="status" className="text-muted-foreground">Loading your access…</p> : <>
      {!project?.can_manage && <p className="text-muted-foreground">Only an owner of this {noun} can change Brain access.</p>}
      <div role="radiogroup" aria-label="Brain access" className="grid gap-2 sm:grid-cols-2">
        {options.map(option => <label key={option.value} className={`flex cursor-pointer flex-col gap-1 rounded-md border px-3 py-2 ${mode === option.value ? 'border-primary bg-primary/5' : 'border-border'} ${blocked ? 'cursor-not-allowed opacity-70' : ''}`}>
          <span className="flex items-center gap-2 font-medium"><input type="radio" name={`brain-access-${workspacePath}`} value={option.value} checked={mode === option.value} disabled={blocked} onChange={() => void changeMode(option.value)}/>{option.label}</span>
          <span className="text-muted-foreground">{option.hint}</span>
        </label>)}
      </div>
    </>}
  </section>
}
