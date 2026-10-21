import { useEffect, useState } from 'react'
import { ArrowRight, UsersRound } from 'lucide-react'
import { Button } from '../../components/ui/Button'
import { knowledgebaseApi, knowledgebaseError, type BrainAccessMode, type KnowledgeProject } from '../../services/knowledgebaseApi'
import { useAuthStore } from '../../stores/useAuthStore'
import { useProductSurfaceStore } from '../../stores/useProductSurfaceStore'
import { visibleProductSurfaceIDs } from '../productSurfaceConfig'
import { openProductWorkspace } from '../../utils/productWorkspaceNavigation'
import { openQuickNavigation, quickNavigationItems } from '../../utils/quickNavigation'
import { loadWorkSessions, type WorkSession } from '../work/workSessions'
import { KnowledgebaseSchedules } from './KnowledgebaseSchedules'

/** A person chooses a Crew and explicitly saves its normal, revision-checked Brain access. */
export function KnowledgebaseUseCrew({ onPreviousChat }: { onPreviousChat: () => void }) {
  const user = useAuthStore(state => state.user)
  const multiUser = useAuthStore(state => state.isMultiUserMode)
  const allowed = visibleProductSurfaceIDs(user?.allowed_products).includes('work')
  const [crews, setCrews] = useState<WorkSession[]>([])
  const [id, setId] = useState('')
  const [project, setProject] = useState<KnowledgeProject | null>(null)
  const [mode, setMode] = useState<BrainAccessMode>('read')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [notice, setNotice] = useState('')
  const [previousOpen, setPreviousOpen] = useState(false)
  const selected = crews.find(item => item.id === id)
  const create = quickNavigationItems(user, 'knowledgebase', multiUser).find(item => item.action === 'create' && item.surface === 'work')
  useEffect(() => {
    let active = true
    if (allowed) void loadWorkSessions().then(items => { if (active) setCrews(items.filter(item => !item.shared)) }).catch(cause => { if (active) setError(knowledgebaseError(cause)) })
    return () => { active = false }
  }, [allowed, user?.id])
  useEffect(() => {
    const controller = new AbortController(); setProject(null); setError(''); setNotice('')
    if (selected && allowed) void knowledgebaseApi.project(selected.workspacePath, controller.signal).then(result => { if (!controller.signal.aborted) { setProject(result); setMode(result.brain_access === 'write' || result.brain_access === 'folders' ? 'write' : result.brain_access === 'off' ? 'read' : result.brain_access ?? 'read') } }).catch(cause => { if (!controller.signal.aborted) setError(knowledgebaseError(cause)) })
    return () => controller.abort()
  }, [selected?.workspacePath, allowed])
  function open(schedules = false) {
    if (!selected || !visibleProductSurfaceIDs(useAuthStore.getState().user?.allowed_products).includes('work')) return
    openProductWorkspace('work'); useProductSurfaceStore.getState().setSelectedWorkProjectId(selected.id)
    if (schedules) useProductSurfaceStore.getState().setPendingWorkView('schedules')
  }
  async function save() {
    if (!selected || !project?.can_manage || busy) return
    setBusy(true); setError(''); setNotice('')
    try {
      const saved = await knowledgebaseApi.setProjectAccess({ workspace_path: selected.workspacePath, mode, expected_manifest_version: project.manifest_version, request_id: crypto.randomUUID() })
      setProject(current => current ? { ...current, ...saved } : null); setNotice('Crew access saved. You can now ask it to work with Brain.')
    } catch (cause) { setError(knowledgebaseError(cause)) }
    finally { setBusy(false) }
  }
  return <section aria-label="Use Brain in a Crew" className="mx-auto max-w-3xl space-y-5 p-4 sm:p-6">
    <div className="flex gap-3"><UsersRound className="mt-1 h-5 w-5 text-muted-foreground" /><div><h2 className="text-lg font-semibold">Put your knowledge to work</h2><p className="mt-1 text-sm text-muted-foreground">Use a Crew to organize knowledge, answer questions or work on a schedule.</p></div></div>
    {allowed ? <div className="space-y-4 rounded-xl border border-border p-4">
      <label className="block space-y-1 text-sm">Choose a Crew<select disabled={busy} value={id} onChange={event => setId(event.target.value)} className="w-full rounded-md border border-border bg-background p-2 text-sm"><option value="">Select an existing Crew</option>{crews.map(item => <option key={item.id} value={item.id}>{item.identity?.name || item.title}</option>)}</select></label>
      {create && <Button variant="outline" size="sm" onClick={() => openQuickNavigation(create)}>Create a Crew</Button>}
      {project && <div className="space-y-3 border-t border-border pt-3"><label className="block space-y-1 text-sm">Brain access<select disabled={!project.can_manage || busy} value={mode} onChange={event => setMode(event.target.value as BrainAccessMode)} className="w-full rounded-md border border-border bg-background p-2"><option value="off">Off</option><option value="read">Read</option><option value="write">Read and write</option></select></label><p className="text-xs text-muted-foreground">The Crew uses its owner’s existing folder permissions. This access applies across the folders that owner can use; it is not limited to the folder currently open in Brain.</p>{project.can_manage ? <Button size="sm" disabled={busy} onClick={() => void save()}>{busy ? 'Saving…' : 'Save Crew access'}</Button> : <p className="text-xs text-muted-foreground">Ask the Crew owner to change its Brain access.</p>}<div className="flex flex-wrap gap-2"><Button variant="outline" size="sm" onClick={() => open()}>Open Crew<ArrowRight className="ml-2 h-3 w-3" /></Button><Button variant="outline" size="sm" onClick={() => open(true)}>Open Crew schedules</Button></div></div>}
    </div> : <p className="rounded-lg border border-border p-4 text-sm text-muted-foreground">Ask an administrator for Crew access to automate work with Brain.</p>}
    {error && <p role="alert" className="text-sm text-destructive">{error}</p>}{notice && <p role="status" className="text-sm text-muted-foreground">{notice}</p>}
    <details onToggle={event => setPreviousOpen(event.currentTarget.open)} className="rounded-lg border border-border p-4"><summary className="w-fit cursor-pointer text-sm text-muted-foreground">Previous Brain activity</summary><div className="mt-3 space-y-3"><p className="text-xs text-muted-foreground">Existing Brain schedules and chat history are retained. Set up the replacement in your Crew, then turn off the old schedule here to avoid duplicate runs. Nothing is moved or started automatically.</p>{previousOpen && <KnowledgebaseSchedules />}<button type="button" onClick={onPreviousChat} className="text-xs text-primary underline">Open previous Brain conversation</button></div></details>
  </section>
}
