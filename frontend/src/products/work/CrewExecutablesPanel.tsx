import { useWorkflowRunNavigation } from './workflowRunNavigation'
import { useCallback, useEffect, useState } from 'react'
import { ArrowLeft, FileCode2, Plus, Zap } from 'lucide-react'
import { isAxiosError } from 'axios'
import api, { agentApi } from '../../services/api'
import { useWorkflowManifestStore } from '../../stores/useWorkflowManifestStore'
import { openWorkspacePaneChatWithDraft } from '../../utils/workspacePaneChat'
import { PageCard, PageRow, RowButton, RunningTag } from '../../components/automation/AutomationKit'
import { CrewExecutableEditor, type CrewExecutable } from './CrewExecutableEditor'
import { CrewWorkflowHeader } from './CrewWorkflowHeader'
import { noRunsView, type RunsView } from './CrewWorkflowRuns'
import { StepsRunsSwitch, type StepsRunsSection } from './StepsRunsSwitch'
import { useLiveRefetch } from '../../hooks/useLiveRefetch'

function failureMessage(error: unknown, fallback: string) {
  if (isAxiosError(error) && typeof error.response?.data === 'string') return error.response.data
  return error instanceof Error ? error.message : fallback
}

/**
 * The Crew's Workflow page. It opens on an overview of every workflow (name, short description and the total);
 * picking one opens its Steps and Runs. "New workflow" starts a new one through the Crew chat.
 * `onBack` returns to the project menu (from the overview), or to the overview (from one workflow).
 */
export function CrewExecutablesPanel({ projectId, chatTabId, onBack }: { projectId: string; chatTabId: string; onBack?: () => void }) {
  // The opened workflow is looked up in the refreshed list, so what the page shows follows edits made in the Crew chat.
  const target = useWorkflowRunNavigation(state => state.target)
  const [selectedId, setSelectedId] = useState<string | null>(null)
  const [expanded, setExpanded] = useState<Set<string>>(new Set())
  const toggleExpanded = (id: string) => setExpanded(current => { const next = new Set(current); if (!next.delete(id)) next.add(id); return next })
  const [section, setSection] = useState<StepsRunsSection>('steps')
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
    setItems([]); setSelectedId(null); setSection('steps'); setRuns(noRunsView); setLoaded(false); setError('')
    void refresh(controller.signal).catch(e => { if (!controller.signal.aborted) setError(failureMessage(e, 'Could not load. Try again.')) })
    return () => controller.abort()
  }, [refresh])
  useEffect(() => {
    if (!loaded || target?.crewId !== projectId) return
    const item = items.find(entry => entry.id === target.executableId && (!target.workspacePath || entry.workspace_path === target.workspacePath))
    if (item) {
      setSelectedId(item.id); setSection(target.section ?? 'runs'); setRuns(target.runFolder ? { run: target.runFolder, costs: false, detail: null } : noRunsView)
    } else setError(target.runFolder ? 'Run view unavailable.' : 'This workflow is not here any more.')
    useWorkflowRunNavigation.setState({ target: null })
  }, [target, loaded, items, projectId])
  // Inputs, saved settings and run steps change when the Crew's agent edits the workflow: read them again then.
  useLiveRefetch(() => { void refresh(new AbortController().signal).catch(() => undefined) }, { kinds: ['sessions', 'plan'], refreshOnFocus: true, fallbackMs: 15_000, safetyMs: 60_000, minIntervalMs: 3_000 })
  async function ask(message: string) {
    try {
      await openWorkspacePaneChatWithDraft({ tabId: chatTabId, message })
    } catch (e) { setError(failureMessage(e, 'Open your project chat to continue.')) }
  }
  // Editing is a chat with the Crew's agent: the draft names the workflow and its ID, and the agent asks what to change.
  const editRequest = (workflow: CrewExecutable) => `I want to edit this workflow: “${workflow.manifest.label}” (workflow ID: ${workflow.id}, path: ${workflow.workspace_path}). Before changing anything, ask me what I want to edit: its name, its description, a task, or a step inside a task. If it is a task or a step, ask me which task and what to change. Wait for my answer, then make the change.\n\nI want to edit: `
  // Delete throws an Error with the server's plain message, which the header shows.
  async function deleteWorkflow(workflow: CrewExecutable) {
    try {
      await api.delete('/api/crew-executables', { params: { crew_id: projectId, id: workflow.id } })
    } catch (e) { throw new Error(failureMessage(e, 'Could not delete the workflow. Try again.')) }
    // The Crew has no workflow now: leave the deleted one's page and show the empty state.
    setSelectedId(null); setSection('steps'); setRuns(noRunsView)
    await refresh(new AbortController().signal).catch(() => setItems([]))
  }
  const item = items.find(entry => entry.id === selectedId) ?? null
  // Which workflows are running right now, asked of the server while the list is on screen.
  const [runningIds, setRunningIds] = useState<Set<string>>(new Set())
  const watching = loaded && !item && items.length > 0
  useEffect(() => {
    if (!watching) return
    let current = true
    const look = () => agentApi.getActiveExecutions().then(({ executions }) => {
      if (current) setRunningIds(new Set(items.filter(entry => (executions ?? []).some(run => run.workspace_path?.includes(entry.id))).map(entry => entry.id)))
    }).catch(() => { /* Not knowing what runs is not worth an error: the rows just show no running mark. */ })
    void look()
    const timer = setInterval(look, 5000)
    return () => { current = false; clearInterval(timer) }
  }, [watching, items])
  // Looks like the header's "New agent" (owner, 19 Nov: the filled button looked like a different app).
  const newWorkflow = <button type="button" onClick={() => void ask('I want to create a new workflow. Ask me what it should do, then build it.')}
    className="inline-flex h-7 shrink-0 items-center gap-1 rounded-md px-2 text-xs text-primary transition-colors hover:bg-primary/10 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring">
    <Plus className="h-3.5 w-3.5" aria-hidden="true" />New workflow
  </button>
  if (item) {
    // Back goes one step at a time: out of a run, then out of the workflow to the project menu,
    // which lists the workflows itself (owner, 19 Nov); the list page is only the way back when there is no menu.
    const inRun = section === 'runs' && (runs.run !== null || runs.costs)
    const back = inRun ? () => setRuns(runs.detail ? { ...runs, detail: null } : noRunsView) : () => { setSelectedId(null); setSection('steps'); onBack?.() }
    return <div className="space-y-4">
      <div className="flex items-center justify-between gap-2">
        <button type="button" aria-label="Back" title="Back" onClick={back} className="flex items-center gap-1 text-xs text-muted-foreground hover:text-foreground"><ArrowLeft className="h-4 w-4" aria-hidden="true" />Back</button>
        {newWorkflow}
      </div>
      <CrewWorkflowHeader key={item.id} name={item.manifest.label} description={item.manifest.description} onEdit={() => void ask(editRequest(item))} onDelete={() => deleteWorkflow(item)} />
      <StepsRunsSwitch section={section} onChange={next => { setRuns(noRunsView); setSection(next) }} />
      <CrewExecutableEditor key={`${item.id}:${runs.run ?? ''}`} item={item} chatTabId={chatTabId} section={section} runs={runs} onRuns={setRuns} onBack={back} onOpenRun={folder => { setSection('runs'); setRuns({ run: folder, costs: false, detail: null }) }} />
    </div>
  }
  const lead = <span className="flex h-8 w-8 items-center justify-center rounded-lg bg-muted"><FileCode2 className="h-4 w-4" aria-hidden="true" /></span>
  return <div className="space-y-4">
    {onBack && <button type="button" aria-label="Back to the project menu" title="Back" onClick={onBack} className="flex items-center gap-1 text-xs text-muted-foreground hover:text-foreground"><ArrowLeft className="h-4 w-4" aria-hidden="true" />Back</button>}
    {error && <p role="alert" className="text-sm text-destructive">{error}</p>}
    {!loaded && !error && <p className="text-sm text-muted-foreground">Loading…</p>}
    {loaded && items.length > 0 && <PageCard testId="crew-workflow-overview" title="Workflows" action={newWorkflow}
      hint={<><span data-testid="crew-workflow-count" className="font-medium text-foreground">{items.length} {items.length === 1 ? 'workflow' : 'workflows'}</span> · Expand one to read about it, or press Open to see its tasks and runs.</>}
      onAsk={() => ask('Help me with the workflows in this project.')}>
      {items.map(workflow => {
        const description = workflow.manifest.description?.trim() || 'No description yet.'
        const isOpen = expanded.has(workflow.id)
        const running = runningIds.has(workflow.id)
        return <PageRow key={workflow.id} testId={`workflow-${workflow.id}`} lead={lead} title={<>{workflow.manifest.label}{running && <RunningTag />}</>} noDivider
          lines={isOpen ? [] : [description]} action={<RowButton title={`Open ${workflow.manifest.label}`} onClick={() => setSelectedId(workflow.id)}>Open</RowButton>}
          open={isOpen} onToggle={() => toggleExpanded(workflow.id)}>
          <p className="whitespace-pre-wrap text-sm text-muted-foreground">{description}</p>
        </PageRow>
      })}
    </PageCard>}
    {loaded && items.length === 0 && <section className="flex flex-col items-center gap-2 rounded-xl border border-border bg-card px-4 py-8 text-center">
      <Zap className="h-5 w-5 text-amber-500" aria-hidden="true" />
      <h3 className="text-sm font-semibold">No workflow in this project yet</h3>
      <p className="text-xs text-muted-foreground">Tell your agent what should happen each time. It builds the workflow and shows it here.</p>
      <RowButton primary onClick={() => void ask('Help me set up a workflow for work I want to repeat.')}>Ask your agent to make one</RowButton>
    </section>}
  </div>
}
