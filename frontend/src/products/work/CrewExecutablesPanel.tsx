import { useCallback, useEffect, useState, type ReactNode } from 'react'
import { FileCode2, Route, Zap } from 'lucide-react'
import { isAxiosError } from 'axios'
import api from '../../services/api'
import { useWorkflowManifestStore } from '../../stores/useWorkflowManifestStore'
import { openWorkspacePaneChatWithDraft } from '../../utils/workspacePaneChat'
import { PageCard, PageRow, RowButton } from '../../components/automation/AutomationKit'
import { CrewExecutableEditor, type CrewExecutable } from './CrewExecutableEditor'
import { PlanRunsSwitch, type PlanRunsSection } from './PlanRunsSwitch'

type StepScript = { workflow_id: string; workflow: string; step_id: string; step: string }

function failureMessage(error: unknown, fallback: string) {
  if (isAxiosError(error) && typeof error.response?.data === 'string') return error.response.data
  return error instanceof Error ? error.message : fallback
}

/**
 * The Crew's workflow (plan and runs, chosen by `section`) with the Scripts card under it.
 * With kind "script", or a Crew that has only scripts, just the Scripts card.
 */
export function CrewExecutablesPanel({ projectId, chatTabId, kind, section, onScriptsOnly }: { projectId: string; chatTabId: string; kind: 'workflow' | 'script'; section?: 'plan' | 'runs'; onScriptsOnly?: (scriptsOnly: boolean) => void }) {
  const [selected, setSelected] = useState<CrewExecutable | null>(null)
  // A step's own script, opened from the Scripts card: a new object per click so the same row can open it again.
  const [stepFile, setStepFile] = useState<{ file: string } | null>(null)
  const [agents, setAgents] = useState<Array<{ id: string; name: string }>>([])
  const [items, setItems] = useState<CrewExecutable[]>([])
  const [stepScripts, setStepScripts] = useState<StepScript[]>([])
  const [error, setError] = useState('')
  const [loaded, setLoaded] = useState(false)
  const refresh = useCallback(async (signal: AbortSignal) => {
    const { data } = await api.get<{ executables: CrewExecutable[]; step_scripts?: StepScript[]; agents: Array<{ id: string; name: string }> }>('/api/crew-executables', { params: { crew_id: projectId }, signal })
    await useWorkflowManifestStore.getState().refreshWorkflows()
    if (signal.aborted) return
    setItems(data.executables)
    setStepScripts(data.step_scripts ?? [])
    setAgents(data.agents)
    setLoaded(true)
    onScriptsOnly?.(!data.executables.some(item => item.kind === 'workflow') && (data.executables.some(item => item.kind === 'script') || (data.step_scripts?.length ?? 0) > 0))
  }, [projectId, onScriptsOnly])
  useEffect(() => {
    const controller = new AbortController()
    setItems([]); setStepScripts([]); setSelected(null); setLoaded(false); setError('')
    void refresh(controller.signal).catch(e => { if (!controller.signal.aborted) setError(failureMessage(e, 'Could not load. Try again.')) })
    return () => controller.abort()
  }, [refresh])
  async function ask(message: string) {
    try {
      await openWorkspacePaneChatWithDraft({ tabId: chatTabId, message })
    } catch (e) { setError(failureMessage(e, 'Open your Crew chat to continue.')) }
  }
  const workflows = items.filter(item => item.kind === 'workflow')
  const scripts = items.filter(item => item.kind === 'script')
  const scriptsCard = <ScriptsCard
    scripts={scripts}
    stepScripts={stepScripts}
    onOpen={setSelected}
    onOpenStep={script => { setSelected(items.find(entry => entry.id === script.workflow_id) ?? null); setStepFile({ file: `code/${script.step_id}/main.py` }) }}
    onAsk={() => ask('Help me create a saved script for this Crew.')}
    onRun={script => ask(`Run the saved script "${script.manifest.label}" and report the result in this chat.`)}
  />
  const single = kind === 'workflow' && workflows.length === 1 ? workflows[0] : null
  const item = selected ?? single
  if (item) {
    const standalone = item !== single
    const editor = (own?: PlanRunsSection) => <CrewExecutableEditor key={item.id} item={item} crewAgents={agents} crewScripts={scripts.map(script => ({ id: script.id, name: script.manifest.label }))} chatTabId={chatTabId} section={standalone ? (own ?? 'plan') : (section ?? 'plan')} openFile={stepFile} onBack={standalone ? () => setSelected(null) : undefined} />
    if (standalone) return <StandaloneExecutable key={item.id} item={item} onBack={() => setSelected(null)} editor={editor} />
    return <div className="space-y-4">{editor()}{section !== 'runs' && scriptsCard}</div>
  }
  const nothing = loaded && workflows.length === 0 && scripts.length === 0 && stepScripts.length === 0
  return <div className="space-y-4">
    {error && <p role="alert" className="text-sm text-destructive">{error}</p>}
    {!loaded && !error && <p className="text-sm text-muted-foreground">Loading…</p>}
    {loaded && <>
      {workflows.length > 1 && <PageCard title="Workflows" hint="This Crew has earlier workflows. Open one to inspect it, or ask chat to combine their tasks into routes." onAsk={() => ask('Help me set up this Crew’s workflow, using routes for different tasks.')}>
        {workflows.map(workflow => <PageRow key={workflow.id} lead={<Route className="h-4 w-4" />} title={workflow.manifest.label} open={false} onToggle={() => setSelected(workflow)}>{null}</PageRow>)}
      </PageCard>}
      {scriptsCard}
      {nothing && <section className="flex flex-col items-center gap-2 rounded-xl border border-border bg-card px-4 py-8 text-center">
        <Zap className="h-5 w-5 text-amber-500" aria-hidden="true" />
        <h3 className="text-sm font-semibold">A Crew with nothing saved yet</h3>
        <p className="text-xs text-muted-foreground">Your agents can save repeatable work as a script or a workflow.</p>
        <RowButton primary onClick={() => void ask('Help me set up this Crew’s workflow or a saved script for work I want to repeat.')}>Ask your agent to make one</RowButton>
      </section>}
    </>}
  </div>
}

/** The Scripts card: every saved script and every script a workflow step runs. */
function ScriptsCard({ scripts, stepScripts, onOpen, onOpenStep, onAsk, onRun }: {
  scripts: CrewExecutable[]
  stepScripts: StepScript[]
  onOpen: (item: CrewExecutable) => void
  onOpenStep: (script: StepScript) => void
  onAsk: () => void | Promise<void>
  onRun: (item: CrewExecutable) => void | Promise<void>
}) {
  if (scripts.length === 0 && stepScripts.length === 0) return null
  const lead = <span className="flex h-8 w-8 items-center justify-center rounded-lg bg-muted"><FileCode2 className="h-4 w-4" aria-hidden="true" /></span>
  return <PageCard title="Scripts" hint="All the runnable code in this Crew." onAsk={onAsk}>
    {scripts.map(script => <PageRow key={script.id} lead={lead} title={script.manifest.label} lines={['Saved script · runs on its own']}
      action={<RowButton onClick={() => void onRun(script)}>Run</RowButton>} open={false} onToggle={() => onOpen(script)}>{null}</PageRow>)}
    {stepScripts.map(script => <PageRow key={script.workflow_id + script.step_id} lead={lead} title={script.step} lines={[`Step script · in ${script.workflow}`]}
      open={false} onToggle={() => onOpenStep(script)}>{null}</PageRow>)}
  </PageCard>
}

/** A script, or a workflow other than the Crew's main one, opened from the Scripts card or the list: the same page, with its own switch. */
function StandaloneExecutable({ item, onBack, editor }: { item: CrewExecutable; onBack: () => void; editor: (section: PlanRunsSection) => ReactNode }) {
  const [section, setSection] = useState<PlanRunsSection>('plan')
  return <div className="space-y-4">
    <PlanRunsSwitch section={section} onChange={setSection} onBack={onBack} />
    {editor(section)}
  </div>
}
