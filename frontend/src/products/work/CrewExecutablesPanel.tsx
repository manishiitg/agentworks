import { useCallback, useEffect, useState } from 'react'
import { Code2, Plus, Route } from 'lucide-react'
import { isAxiosError } from 'axios'
import api from '../../services/api'
import { useWorkflowManifestStore } from '../../stores/useWorkflowManifestStore'
import { CrewExecutableEditor, type CrewExecutable } from './CrewExecutableEditor'

type Executable = CrewExecutable

function failureMessage(error: unknown, fallback: string) {
  if (isAxiosError(error) && typeof error.response?.data === 'string') return error.response.data
  return error instanceof Error ? error.message : fallback
}

/** Crew owns the definitions; their existing editors and runners remain authoritative. */
export function CrewExecutablesPanel({ projectId, chatTabId }: { projectId: string; chatTabId: string }) {
  const [selected, setSelected] = useState<Executable | null>(null)
  const [agents, setAgents] = useState<Array<{ id: string; name: string }>>([])
  const [items, setItems] = useState<Executable[]>([])
  const [name, setName] = useState('')
  const [kind, setKind] = useState<'workflow' | 'relay'>('workflow')
  const [creating, setCreating] = useState(false)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [loaded, setLoaded] = useState(false)
  const refresh = useCallback(async (signal?: AbortSignal) => {
    const { data } = await api.get<{ executables: Executable[]; agents: Array<{ id: string; name: string }> }>('/api/crew-executables', { params: { crew_id: projectId }, signal })
    setItems(data.executables)
    setAgents(data.agents)
    setLoaded(true)
  }, [projectId])
  useEffect(() => {
    const controller = new AbortController()
    setItems([]); setLoaded(false); setError('')
    void refresh(controller.signal).catch(e => { if (!controller.signal.aborted) setError(failureMessage(e, 'Could not load. Try again.')) })
    return () => controller.abort()
  }, [refresh])
  async function create() {
    setBusy(true); setError('')
    try {
      await api.post('/api/crew-executables', { name, kind }, { params: { crew_id: projectId } })
      await refresh()
      setCreating(false); setName('')
    } catch (e) { setError(failureMessage(e, 'Could not save. Try again.')) }
    finally { setBusy(false) }
  }
  async function open(item: Executable) {
    setBusy(true); setError('')
    try {
      await useWorkflowManifestStore.getState().refreshWorkflows()
      setSelected(item)
    } catch (e) { setError(e instanceof Error ? e.message : 'Could not open.') }
    finally { setBusy(false) }
  }
  if (selected) return <CrewExecutableEditor key={selected.id} item={selected} crewAgents={agents} chatTabId={chatTabId} onBack={() => setSelected(null)} />
  return <div className="mx-auto w-full max-w-3xl space-y-6 p-6">
    <div className="flex items-center justify-between gap-4">
      <p className="text-sm text-muted-foreground">Repeat a sequence of steps, or call a saved program.</p>
      <button onClick={() => setCreating(true)} className="inline-flex items-center gap-2 rounded-md border px-3 py-2 text-sm"><Plus className="h-4 w-4" />Add</button>
    </div>
    {error && <p role="alert" className="text-sm text-destructive">{error}</p>}
    {creating && <form onSubmit={e => { e.preventDefault(); void create() }} className="space-y-3 rounded-lg border p-4">
      <label className="block text-sm">Name<input autoFocus required maxLength={120} value={name} onChange={e => setName(e.target.value)} className="mt-1 w-full rounded-md border bg-background p-2" /></label>
      <label className="block text-sm">Type<select value={kind} onChange={e => setKind(e.target.value as typeof kind)} className="ml-3 rounded-md border bg-background p-2"><option value="workflow">Workflow</option><option value="relay">Program (Relay)</option></select></label>
      <div className="flex gap-3"><button disabled={busy || !name.trim()} className="rounded-md bg-primary px-3 py-2 text-sm text-primary-foreground">{busy ? 'Saving…' : 'Create'}</button><button type="button" onClick={() => setCreating(false)} className="text-sm">Cancel</button></div>
    </form>}
    {!loaded && !error && <p className="text-sm text-muted-foreground">Loading…</p>}
    {loaded && items.length === 0 && !creating && <p className="text-sm text-muted-foreground">Nothing added yet. Your Crew can keep working in chat without a workflow.</p>}
    <ul className="divide-y divide-border">{items.map(item => <li key={item.id}><button disabled={busy} onClick={() => void open(item)} className="flex w-full items-center gap-3 py-4 text-left hover:text-primary">{item.kind === 'relay' ? <Code2 className="h-5 w-5" /> : <Route className="h-5 w-5" />}<span className="flex-1">{item.manifest.label}</span><span className="text-xs text-muted-foreground">{item.kind === 'relay' ? 'Program' : 'Workflow'}</span></button></li>)}</ul>
  </div>
}
