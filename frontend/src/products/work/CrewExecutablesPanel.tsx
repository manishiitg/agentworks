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

function failureMessage(error: unknown, fallback: string) {
  if (isAxiosError(error) && typeof error.response?.data === 'string') return error.response.data
  return error instanceof Error ? error.message : fallback
}

/**
 * The Crew's Workflow page. One workflow opens straight onto its Steps and Runs; several are listed first.
 * `onBack` returns to the project menu (from the list), or to the list (from one workflow).
 */
export function CrewExecutablesPanel({ projectId, chatTabId, onBack }: { projectId: string; chatTabId: string; onBack?: () => void }) {
  const [selected, setSelected] = useState<CrewExecutable | null>(null)
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
    setItems([]); setSelected(null); setSection('steps'); setFile(null); setRuns(noRunsView); setLoaded(false); setError('')
    void refresh(controller.signal).catch(e => { if (!controller.signal.aborted) setError(failureMessage(e, 'Could not load. Try again.')) })
    return () => controller.abort()
  }, [refresh])
  async function ask(message: string) {
    try {
      await openWorkspacePaneChatWithDraft({ tabId: chatTabId, message })
    } catch (e) { setError(failureMessage(e, 'Open your Crew chat to continue.')) }
  }
  const item = selected ?? (items.length === 1 ? items[0] : null)
  if (item) {
    // Back goes one step at a time: out of an open file, then out of a workflow to the list, then to the project menu.
    const inRun = section === 'runs' && (runs.run !== null || runs.costs)
    const back = file ? () => setFile(null) : inRun ? () => setRuns(noRunsView) : selected ? () => { setSelected(null); setSection('steps') } : onBack
    return <div className="space-y-4">
      <StepsRunsSwitch section={section} onChange={next => { setFile(null); setRuns(noRunsView); setSection(next) }} onBack={back} />
      <CrewExecutableEditor key={item.id} item={item} chatTabId={chatTabId} section={section} file={file} onFile={setFile} runs={runs} onRuns={setRuns} onBack={back} />
    </div>
  }
  const lead = <span className="flex h-8 w-8 items-center justify-center rounded-lg bg-muted"><FileCode2 className="h-4 w-4" aria-hidden="true" /></span>
  return <div className="space-y-4">
    {onBack && <button type="button" aria-label="Back to the project menu" title="Back" onClick={onBack} className="flex items-center gap-1 text-xs text-muted-foreground hover:text-foreground"><ArrowLeft className="h-4 w-4" aria-hidden="true" />Back</button>}
    {error && <p role="alert" className="text-sm text-destructive">{error}</p>}
    {!loaded && !error && <p className="text-sm text-muted-foreground">Loading…</p>}
    {loaded && items.length > 0 && <PageCard title="Workflows" hint="Open one to see its steps and runs." onAsk={() => ask('Help me with the workflows in this Crew.')}>
      {items.map(workflow => <PageRow key={workflow.id} lead={lead} title={workflow.manifest.label} open={false} onToggle={() => setSelected(workflow)}>{null}</PageRow>)}
    </PageCard>}
    {loaded && items.length === 0 && <section className="flex flex-col items-center gap-2 rounded-xl border border-border bg-card px-4 py-8 text-center">
      <Zap className="h-5 w-5 text-amber-500" aria-hidden="true" />
      <h3 className="text-sm font-semibold">No workflow in this Crew yet</h3>
      <p className="text-xs text-muted-foreground">Tell your agent what should happen each time. It builds the workflow and shows it here.</p>
      <RowButton primary onClick={() => void ask('Help me set up a workflow for work I want to repeat.')}>Ask your agent to make one</RowButton>
    </section>}
  </div>
}
