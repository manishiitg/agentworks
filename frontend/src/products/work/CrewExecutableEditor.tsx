import { isAxiosError } from 'axios'
import { Suspense, useEffect, useMemo, useState } from 'react'
import api from '../../services/api'
import { CrewWorkflowRuns, type RunsView } from './CrewWorkflowRuns'
import { CrewFileView } from './CrewFileView'
import { CrewWorkflowSteps } from './CrewWorkflowSteps'
import { PageCard, PageRow, RowButton } from '../../components/automation/AutomationKit'
import { FileCode2, FileJson } from 'lucide-react'
import type { RunPythonStep } from '../../services/api-types'
import { useWorkspaceState } from '../../components/workflow/hooks/useWorkspaceState'
import { useLiveRefetch } from '../../hooks/useLiveRefetch'
import { openWorkspacePaneChatWithDraft } from '../../utils/workspacePaneChat'

export type CrewExecutable = {
  id: string
  kind: 'workflow' | 'script'
  workspace_path: string
  manifest: { label: string }
  used_by?: Array<{ workflow: string; step: string }>
  /** What a person can change before a run, and the saved settings that fill it in. */
  inputs?: Array<{ name: string; value: string; description?: string; type?: string; required?: boolean }>
  input_presets?: Array<{ name: string; values: Record<string, string> }>
}

const inputKind = (type?: string) => ({ number: 'Number', integer: 'Number', boolean: 'Yes or no', array: 'List', object: 'Details' } as Record<string, string>)[type ?? ''] ?? 'Text'

/** The Crew keeps its chat. Only the selected definition/run changes in this pane. */
export function CrewExecutableEditor({
  item,
  chatTabId,
  section,
  file,
  onFile,
  runs,
  onRuns,
  onStarted,
  onBack,
}: {
  item: CrewExecutable
  chatTabId: string
  /** The page owns the Steps | Runs switch (StepsRunsSwitch) and which file is open, so its back arrow can close the file first. */
  section: 'steps' | 'runs'
  file: string | null
  onFile: (file: string | null) => void
  runs: RunsView
  onRuns: (view: RunsView) => void
  /** A run was started: the page moves to its Runs. */
  onStarted: () => void
  onBack?: () => void
}) {
  const [error, setError] = useState('')
  const [starting, setStarting] = useState(false)
  const workspace = useWorkspaceState(item.workspace_path, null, false)
  // A run may have started while this workflow page was closed. Do not rely on its mount cache.
  useEffect(() => { if (runs.run) void workspace.refresh() }, [runs.run, workspace.refresh])
  const folders = workspace.state?.run_folders ?? []
  // The steps of the latest run, in the order they started: the dots on the Steps page.
  const latestRun = useMemo(() => {
    const latest = [...folders].filter(folder => folder.metadata?.python_steps).sort((a, b) => (b.metadata?.created_at ?? '').localeCompare(a.metadata?.created_at ?? ''))[0]
    return latest
  }, [folders])
  const latestSteps: RunPythonStep[] = Object.values(latestRun?.metadata?.python_steps ?? {}).sort((a, b) => a.started_at.localeCompare(b.started_at))
  useLiveRefetch(
    () => {
      void workspace.refresh()
    },
    { kinds: ['sessions', 'plan'], workflow: item.workspace_path, fallbackMs: 15_000, minIntervalMs: 1000 },
  )
  // Run starts the workflow directly, the same way a schedule does: no chat and no model turn.
  async function run(preset?: string) {
    setError(''); setStarting(true)
    try {
      await api.post('/api/workflow/run', preset ? { input_preset: preset } : {}, { params: { workspace_path: item.workspace_path } })
      onStarted()
    } catch (cause) {
      // The route answers with plain text, or with the platform's {error: {message}} (for example an input that fails validation).
      const data: unknown = isAxiosError(cause) ? cause.response?.data : undefined
      const message = typeof data === 'string' ? data : (data as { error?: { message?: string } } | undefined)?.error?.message ?? ''
      const nextStep = /missing required input/i.test(message)
        ? item.input_presets?.length
          ? ' Pick saved settings from Run with, or run it from chat or the CLI with inputs.'
          : ' Run it from chat or the CLI with inputs, or ask your agent to save a preset.'
        : ''
      setError((message || 'Could not start the workflow. Try again.') + nextStep)
    } finally { setStarting(false) }
  }
  async function ask(task: string) {
    setError('')
    try {
      await openWorkspacePaneChatWithDraft({
        tabId: chatTabId,
        message: `${task}\n\nWorkflow: ${item.manifest.label}\nWorkspace: ${item.workspace_path}`,
      })
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : 'Open your Crew chat to continue.')
    }
  }
  const script = item.kind === 'script'
  return (
    <div className="space-y-4">
      {script && !!item.used_by?.length && <p className="border-b px-3 py-2 text-xs text-muted-foreground">Used by {Array.from(new Set(item.used_by.map(use => use.workflow))).join(', ')}. Changes here apply to all of them.</p>}
      {(error || workspace.error) && (
        <p role="alert" className="p-3 text-sm text-destructive">
          {error || workspace.error}
        </p>
      )}
      <Suspense fallback={<p className="p-4 text-sm text-muted-foreground">Loading…</p>}>
        {file ? (
          <CrewFileView key={file} workspacePath={item.workspace_path} file={file} onClose={() => onFile(null)} onAsk={ask} />
        ) : section === 'steps' ? (
          !script ? (
            <PageCard title={item.manifest.label} testId="crew-old-workflow-card">
              <p className="px-4 py-3 text-sm text-muted-foreground">This workflow was made in the old format. Ask your agent to convert it to a new workflow; what it does stays the same.</p>
              <div className="px-4 pb-3"><RowButton primary onClick={() => void ask('Convert this workflow to a new workflow and keep what it does.')}>Ask your agent to convert it</RowButton></div>
            </PageCard>
          ) : (
            <div className="space-y-4">
              <CrewWorkflowSteps workspacePath={item.workspace_path} title={item.manifest.label} latestSteps={latestSteps} latestEvidence={latestRun?.metadata?.python_evidence ?? []}
                onRun={preset => void run(preset)} runPresets={item.input_presets?.map(preset => preset.name)} runBusy={starting} onCode={() => onFile('main.py')} onAsk={ask} />
              {!!(item.inputs?.length || item.input_presets?.length) && <PageCard title="Inputs" hint="What you can change before a run." onAsk={() => ask('Help me change the inputs of this workflow.')}>
                {item.inputs?.map(input => <PageRow key={input.name} title={input.name}
                  lines={[`${inputKind(input.type)}${input.required ? ' · required' : ''}${input.value ? ` · default “${input.value}”` : ''}`, input.description]} />)}
                {!!item.input_presets?.length && <PageRow title="Saved settings" lines={[item.input_presets.map(preset => preset.name).join(' · ')]} />}
              </PageCard>}
              <PageCard title="Files" hint="The workflow's code and what it uses." onAsk={() => ask('Help me understand the files of this workflow.')}>
                <PageRow lead={<span className="flex h-8 w-8 items-center justify-center rounded-lg bg-muted"><FileCode2 className="h-4 w-4" aria-hidden="true" /></span>} title="main.py" lines={['The workflow']} action={<RowButton onClick={() => onFile('main.py')}>View</RowButton>} />
                <PageRow lead={<span className="flex h-8 w-8 items-center justify-center rounded-lg bg-muted"><FileJson className="h-4 w-4" aria-hidden="true" /></span>} title="script.json" lines={['Name, inputs and saved settings']} action={<RowButton onClick={() => onFile('script.json')}>View</RowButton>} />
              </PageCard>
            </div>
          )
        ) : (
          <CrewWorkflowRuns workspacePath={item.workspace_path} script={script} view={runs} onView={onRuns} folders={folders} loading={workspace.loading || !workspace.state} onRefresh={workspace.refresh} onAsk={ask} />
        )}
      </Suspense>
    </div>
  )
}
