import { useWorkflowRunNavigation } from './workflowRunNavigation'
import { useCallback, useEffect, useState } from 'react'
import { ArrowLeft, FileCode2, Zap } from 'lucide-react'
import { isAxiosError } from 'axios'
import api from '../../services/api'
import { useWorkflowManifestStore } from '../../stores/useWorkflowManifestStore'
import { openWorkspacePaneChatWithDraft } from '../../utils/workspacePaneChat'
import { PageCard, PageRow, RowButton } from '../../components/automation/AutomationKit'
import { CrewExecutableEditor, type CrewExecutable } from './CrewExecutableEditor'
import { noRunsView, type RunsView } from './CrewWorkflowRuns'
import { StepsRunsSwitch, type StepsRunsSection } from './StepsRunsSwitch'
import { useLiveRefetch } from '../../hooks/useLiveRefetch'

function failureMessage(error: unknown, fallback: string) {
  if (isAxiosError(error) && typeof error.response?.data === 'string') return error.response.data
  return error instanceof Error ? error.message : fallback
}

/**
 * The Crew's Workflow page. One workflow opens straight onto its Steps and Runs; several are listed first.
 * `onBack` returns to the project menu (from the list), or to the list (from one workflow).
 */
export function CrewExecutablesPanel({ projectId, chatTabId, onBack }: { projectId: string; chatTabId: string; onBack?: () => void }) {
  // The opened workflow is looked up in the refreshed list, so what the page shows follows edits made in the Crew chat.
  const target = useWorkflowRunNavigation(state => state.target)
  const [selectedId, setSelectedId] = useState<string | null>(null)
  const [section, setSection] = useState<StepsRunsSection>('steps')
  const [file, setFile] = useState<string | null>(null)
  const [runs, setRuns] = useState<RunsView>(noRunsView)
  const [items, setItems] = useState<CrewExecutable[]>([])
  const [error, setError] = useState('')
  const [loaded, setLoaded] = useState(false)
  const refresh = useCallback(async (signal: AbortSignal) => {
    const { data } = await api.get<{ executables: CrewExecutable[] }>('/api/crew-executables', { params: { crew_id: projectId }, signal })
    await useWorkflowManifestStore.getState().refreshWorkflows()
    if (signal.aborted) return
    setItems(data.executables)
    setLoaded(true)
  }, [projectId])
  useEffect(() => {
    const controller = new AbortController()
    setItems([]); setSelectedId(null); setSection('steps'); setFile(null); setRuns(noRunsView); setLoaded(false); setError('')
    void refresh(controller.signal).catch(e => { if (!controller.signal.aborted) setError(failureMessage(e, 'Could not load. Try again.')) })
    return () => controller.abort()
  }, [refresh])
  useEffect(() => {
    if (!loaded || target?.crewId !== projectId) return
    const item = items.find(entry => entry.id === target.executableId && entry.workspace_path === target.workspacePath)
    if (item) {
      setSelectedId(item.id); setSection('runs'); setFile(null); setRuns({ run: target.runFolder, costs: false, detail: null })
    } else setError('Run view unavailable.')
    useWorkflowRunNavigation.setState({ target: null })
  }, [target, loaded, items, projectId])
  // Inputs, saved settings and run steps change when the Crew's agent edits the workflow: read them again then.
  useLiveRefetch(() => { void refresh(new AbortController().signal).catch(() => undefined) }, { kinds: ['sessions', 'plan'], refreshOnFocus: true, fallbackMs: 15_000, safetyMs: 60_000, minIntervalMs: 3_000 })
  async function ask(message: string) {
    try {
      await openWorkspacePaneChatWithDraft({ tabId: chatTabId, message })
    } catch (e) { setError(failureMessage(e, 'Open your Crew chat to continue.')) }
  }
  const selected = items.find(entry => entry.id === selectedId) ?? null
  const item = selected ?? (items.length === 1 ? items[0] : null)
  if (item) {
    // Back goes one step at a time: out of an open file, then out of a workflow to the list, then to the project menu.
    const inRun = section === 'runs' && (runs.run !== null || runs.costs)
    const back = file ? () => setFile(null) : inRun ? () => setRuns(runs.detail ? { ...runs, detail: null } : noRunsView) : selected ? () => { setSelectedId(null); setSection('steps') } : onBack
    return <div className="space-y-4">
      <StepsRunsSwitch section={section} onChange={next => { setFile(null); setRuns(noRunsView); setSection(next) }} onBack={back} />
      <CrewExecutableEditor key={`${item.id}:${runs.run ?? ''}`} item={item} chatTabId={chatTabId} section={section} file={file} onFile={setFile} runs={runs} onRuns={setRuns} onBack={back} />
    </div>
  }
  const lead = <span className="flex h-8 w-8 items-center justify-center rounded-lg bg-muted"><FileCode2 className="h-4 w-4" aria-hidden="true" /></span>
  return <div className="space-y-4">
    {onBack && <button type="button" aria-label="Back to the project menu" title="Back" onClick={onBack} className="flex items-center gap-1 text-xs text-muted-foreground hover:text-foreground"><ArrowLeft className="h-4 w-4" aria-hidden="true" />Back</button>}
    {error && <p role="alert" className="text-sm text-destructive">{error}</p>}
    {!loaded && !error && <p className="text-sm text-muted-foreground">Loading…</p>}
    {loaded && items.length > 0 && <PageCard title="Workflows" hint="Open one to see its steps and runs." onAsk={() => ask('Help me with the workflows in this Crew.')}>
      {items.map(workflow => <PageRow key={workflow.id} lead={lead} title={workflow.manifest.label} open={false} onToggle={() => setSelectedId(workflow.id)}>{null}</PageRow>)}
    </PageCard>}
    {loaded && items.length === 0 && <section className="flex flex-col items-center gap-2 rounded-xl border border-border bg-card px-4 py-8 text-center">
      <Zap className="h-5 w-5 text-amber-500" aria-hidden="true" />
      <h3 className="text-sm font-semibold">No workflow in this Crew yet</h3>
      <p className="text-xs text-muted-foreground">Tell your agent what should happen each time. It builds the workflow and shows it here.</p>
      <RowButton primary onClick={() => void ask('Help me set up a workflow for work I want to repeat.')}>Ask your agent to make one</RowButton>
    </section>}
  </div>
}
