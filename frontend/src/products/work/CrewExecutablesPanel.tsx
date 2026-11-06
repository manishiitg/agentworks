import { useCallback, useEffect, useState } from 'react'
import { FileCode2, Route } from 'lucide-react'
import { isAxiosError } from 'axios'
import api from '../../services/api'
import { useWorkflowManifestStore } from '../../stores/useWorkflowManifestStore'
import { openWorkspacePaneChatWithDraft } from '../../utils/workspacePaneChat'
import { CrewExecutableEditor, type CrewExecutable } from './CrewExecutableEditor'

function failureMessage(error: unknown, fallback: string) {
  if (isAxiosError(error) && typeof error.response?.data === 'string') return error.response.data
  return error instanceof Error ? error.message : fallback
}

/** One workflow, with routes. Its existing steps and runs open directly. */
export function CrewExecutablesPanel({ projectId, chatTabId, kind }: { projectId: string; chatTabId: string; kind: 'workflow' | 'script' }) {
  const [selected, setSelected] = useState<CrewExecutable | null>(null)
  const [agents, setAgents] = useState<Array<{ id: string; name: string }>>([])
  const [items, setItems] = useState<CrewExecutable[]>([])
  const [error, setError] = useState('')
  const [loaded, setLoaded] = useState(false)
  const refresh = useCallback(async (signal: AbortSignal) => {
    const { data } = await api.get<{ executables: CrewExecutable[]; agents: Array<{ id: string; name: string }> }>('/api/crew-executables', { params: { crew_id: projectId }, signal })
    await useWorkflowManifestStore.getState().refreshWorkflows()
    if (signal.aborted) return
    setItems(data.executables)
    setAgents(data.agents)
    setLoaded(true)
  }, [projectId])
  useEffect(() => {
    const controller = new AbortController()
    setItems([]); setSelected(null); setLoaded(false); setError('')
    void refresh(controller.signal).catch(e => { if (!controller.signal.aborted) setError(failureMessage(e, 'Could not load. Try again.')) })
    return () => controller.abort()
  }, [refresh])
  async function ask() {
    try {
      await openWorkspacePaneChatWithDraft({ tabId: chatTabId, message: kind === 'script' ? 'Help me create a saved script for this Crew.' : 'Help me set up this Crew’s workflow, using routes for different tasks.' })
    } catch (e) { setError(failureMessage(e, 'Open your Crew chat to continue.')) }
  }
  const visibleItems = items.filter(item => item.kind === kind)
  const item = kind === 'workflow' && visibleItems.length === 1 ? visibleItems[0] : selected
  if (item) return <CrewExecutableEditor key={item.id} item={item} crewAgents={agents} crewScripts={items.filter(item => item.kind === 'script').map(item => ({ id: item.id, name: item.manifest.label }))} chatTabId={chatTabId} onBack={kind === 'script' || visibleItems.length > 1 ? () => setSelected(null) : undefined} />
  return <div className="mx-auto w-full max-w-3xl space-y-4 p-6">
    {error && <p role="alert" className="text-sm text-destructive">{error}</p>}
    {!loaded && !error && <p className="text-sm text-muted-foreground">Loading…</p>}
    {loaded && <>
      {kind === 'workflow' && visibleItems.length > 1 && <p className="text-sm text-muted-foreground">This Crew has earlier workflows. Open one to inspect it, or ask chat to combine their tasks into routes.</p>}
      {visibleItems.length === 0 && <p className="text-sm text-muted-foreground">{kind === 'script' ? 'No saved scripts yet.' : 'No workflow yet. Describe the work you want to repeat in chat.'}</p>}
      <ul className="divide-y divide-border">{visibleItems.map(item => <li key={item.id}><button onClick={() => setSelected(item)} className="flex w-full items-center gap-3 py-4 text-left hover:text-primary">{item.kind === 'workflow' ? <Route className="h-5 w-5" /> : <FileCode2 className="h-5 w-5" />}<span>{item.manifest.label}</span></button></li>)}</ul>
      <button onClick={() => void ask()} className="text-sm text-primary">{kind === 'script' ? 'Create a script in chat' : visibleItems.length ? 'Discuss in chat' : 'Set up in chat'}</button>
    </>}
  </div>
}
