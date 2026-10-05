import { useCallback, useEffect, useRef, useState } from 'react'
import { BookOpen, RefreshCw } from 'lucide-react'
import { Button } from '../ui/Button'
import { Checkbox } from '../ui/checkbox'
import { knowledgebaseApi, knowledgebaseError, type KnowledgeFolder, type KnowledgeProject, type KnowledgeBinding } from '../../services/knowledgebaseApi'
import { AskAIButton } from './AskAIButton'
import { WORKFLOW_KNOWLEDGE_SOURCES_REFRESH_EVENT } from './workflowEvents'

// Same selection pattern as ProjectVaultPanel: selecting a resource never
// grants permission. Backend owner, audience and current folder checks apply.
export function ProjectKnowledgebasePanel({ workspacePath, disabled = false }: { workspacePath: string; disabled?: boolean }) {
  const [project, setProject] = useState<KnowledgeProject | null>(null)
  const [folders, setFolders] = useState<KnowledgeFolder[]>([])
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
      const all: KnowledgeFolder[] = []
      let cursor = ''
      do {
        const page = await knowledgebaseApi.projectFolders(cursor)
        all.push(...page.items.filter(folder => folder.folder_id && !folder.breadcrumb_only && folder.effective_role))
        cursor = page.next_cursor || ''
      } while (cursor && version === generation.current)
      if (version === generation.current) { setProject(current); setFolders(all); setAvailable(true); setError('') }
    } catch (cause) {
      if (version === generation.current) {
        if ((cause as { response?: { status?: number } }).response?.status === 404) setAvailable(false)
        else setError(knowledgebaseError(cause))
      }
    } finally { if (version === generation.current) setLoading(false) }
  }, [workspacePath])
  useEffect(() => {
    setProject(null); setFolders([]); setError(''); setAvailable(true)
    void refresh()
    const changed = (event: Event) => {
      const detail = (event as CustomEvent<{ workspacePath?: string }>).detail
      if (!detail || detail.workspacePath === workspacePath) void refresh()
    }
    window.addEventListener('focus', changed)
    window.addEventListener(WORKFLOW_KNOWLEDGE_SOURCES_REFRESH_EVENT, changed)
    return () => { generation.current++; window.removeEventListener('focus', changed); window.removeEventListener(WORKFLOW_KNOWLEDGE_SOURCES_REFRESH_EVENT, changed) }
  }, [refresh, workspacePath])
  const bindings = project?.shared_knowledgebase || []
  const save = async (folder: KnowledgeFolder | undefined, binding: KnowledgeBinding | undefined, access?: 'read' | 'write') => {
    if (!project?.can_manage || disabled || loading || busy) return
    const version = generation.current
    setBusy(true); setError('')
    let alias = binding?.alias || `kb_${(folder?.path || 'folder').toLowerCase().replace(/[^a-z0-9_]/g, '_').slice(-35)}`
    if (!binding) { let suffix = 2; const base = alias; while (bindings.some(item => item.alias === alias)) alias = `${base}_${suffix++}` }
    try {
      await knowledgebaseApi.bindProject({
        action: folder && (!binding || access) ? 'bind_project' : 'unbind_project',
        workspace_path: workspacePath, alias,
        ...(folder && (!binding || access) ? { folder_id: folder.folder_id, access: access || 'read' } : {}),
        expected_manifest_version: project.manifest_version, request_id: crypto.randomUUID(),
      })
      if (version === generation.current) {
        window.dispatchEvent(new CustomEvent(WORKFLOW_KNOWLEDGE_SOURCES_REFRESH_EVENT, { detail: { workspacePath } }))
      }
    } catch (cause) { if (version === generation.current) setError(knowledgebaseError(cause)) }
    finally { setBusy(false) }
  }
  if (!available) return null
  const blocked = disabled || !project?.can_manage || busy || loading
  const missing = bindings.filter(binding => !folders.some(folder => folder.folder_id === binding.folder_id))
  return <section aria-label="Brain folders" className="space-y-3 rounded-lg border border-border p-4 text-xs">
    <div className="flex flex-wrap items-center justify-between gap-2">
      <h3 className="flex items-center gap-2 text-sm font-semibold"><BookOpen className="h-4 w-4 text-primary"/>Brain folders</h3>
      <div className="flex items-center gap-1"><AskAIButton workspacePath={disabled ? null : workspacePath} label="Ask AI to add" message="Attach a shared Brain folder to this workflow. Use browse_knowledgebase action=folders to find folders, inspect_project for the current manifest version, then bind_project with read access unless I request read-write. Confirm that the workflow audience already has access. Configure relevant steps with knowledgebase_access=read or read-write; writes need a knowledgebase_contribution. Do not change folder grants implicitly."/><Button variant="ghost" size="icon" className="h-7 w-7" aria-label="Refresh Brain folders" disabled={loading || busy} onClick={() => void refresh()}><RefreshCw className={loading ? 'animate-spin' : ''}/></Button></div>
    </div>
    <p className="text-muted-foreground">Select folders for this workflow. Selection uses existing access; it does not grant permission. Steps use their own read/write settings through MCP.</p>
    {error && <p role="alert" className="text-destructive">{error}</p>}
    {loading ? <p role="status" className="text-muted-foreground">Loading your access…</p> : <>
      {!project?.can_manage && <p className="text-muted-foreground">Only a workflow owner can change these selections.</p>}
      {folders.length === 0 && <p className="text-muted-foreground">No Brain folders available.</p>}
      {folders.map(folder => {
        const binding = bindings.find(item => item.folder_id === folder.folder_id)
        return <div key={folder.folder_id} className="flex flex-wrap items-center gap-2 rounded-md border border-border px-3 py-2">
          <Checkbox aria-label={`Use ${folder.path}`} checked={!!binding} disabled={blocked} onCheckedChange={() => void save(folder, binding)}/>
          <span className="min-w-0 flex-1 break-all">{folder.path}</span>
          {binding && <><code className="text-muted-foreground">{binding.alias}</code><select aria-label={`Access for ${folder.path}`} value={binding.access} disabled={blocked} className="rounded border border-border bg-background px-2 py-1" onChange={event => void save(folder, binding, event.target.value as 'read' | 'write')}><option value="read">Read only</option><option value="write" disabled={folder.effective_role === 'reader'}>Read-write</option></select></>}
        </div>
      })}
      {missing.map(binding => <div key={binding.alias} className="flex items-center justify-between gap-2"><span>{binding.alias} · No longer available</span><Button variant="ghost" size="xs" disabled={blocked} onClick={() => void save(undefined, binding)}>Remove selection</Button></div>)}
    </>}
  </section>
}
