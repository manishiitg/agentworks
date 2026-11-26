import { Suspense, useEffect, useMemo, useState } from 'react'
import api from '../../services/api'
import { CrewWorkflowRuns, type RunsView } from './CrewWorkflowRuns'
import { requestOpenProjectFile } from '../../utils/projectFileLinks'
import { CrewWorkflowSteps } from './CrewWorkflowSteps'
import { CrewWorkflowTasks, runTaskName, taskFileName } from './CrewWorkflowTasks'
import { CrewWorkflowFiles } from './CrewWorkflowFiles'
import { CrewWorkflowStarts } from './CrewWorkflowStarts'
import { CrewWorkflowUsesBlock, useCrewWorkflowUses } from './CrewWorkflowUses'
import { PageCard } from '../../components/automation/AutomationKit'
import type { StepsRunsSection } from './StepsRunsSwitch'
import { CrewRunBanner } from './CrewRunBanner'
import { useRunWatch } from './useRunWatch'
import { callExternal } from '../../api/externalCall'
import { isAxiosError } from 'axios'
import type { RunPythonStep } from '../../services/api-types'
import { useWorkspaceState } from '../../components/workflow/hooks/useWorkspaceState'
import { useLiveRefetch } from '../../hooks/useLiveRefetch'
import { openWorkspacePaneChatWithDraft } from '../../utils/workspacePaneChat'

export type CrewExecutable = {
  id: string
  kind: 'script'
  workspace_path: string
  manifest: { label: string; /** One short line about what the workflow is for; may be empty. */ description?: string }
  /** What a person can change before a run, and the saved settings that fill it in. */
  inputs?: Array<{ name: string; value: string; description?: string; type?: string; required?: boolean; schema?: { enum?: unknown[]; descriptions?: Record<string, unknown> } }>
  input_presets?: Array<{ name: string; values: Record<string, string> }>
  files?: string[]
}

/**
 * A Crew can have several workflows, and each can hold several tasks that a `task` input picks between.
 * Returns those task names (the values the `task` input allows), or an empty list when it has none.
 */
export const workflowTaskNames = (inputs: CrewExecutable['inputs']): string[] => {
  const allowed = inputs?.find(input => input.name === 'task')?.schema?.enum
  return Array.isArray(allowed) ? allowed.filter((name): name is string => typeof name === 'string' && name !== '') : []
}

/** One plain line per task, from the `task` input's `schema.descriptions` (task name to sentence). Tasks without one are left out. */
export const workflowTaskDescriptions = (inputs: CrewExecutable['inputs']): Record<string, string> => {
  const written = inputs?.find(input => input.name === 'task')?.schema?.descriptions
  if (!written || typeof written !== 'object') return {}
  return Object.fromEntries(Object.entries(written).filter((entry): entry is [string, string] => typeof entry[1] === 'string' && entry[1].trim() !== '').map(([task, text]) => [task, text.trim()]))
}

/** The Crew keeps its chat. Only the selected definition/run changes in this pane. */
export function CrewExecutableEditor({
  item,
  chatTabId,
  section,
  runs,
  onRuns,
  onBack,
  onOpenRun,
}: {
  item: CrewExecutable
  chatTabId: string
  /** The page owns the Tasks | Runs switch (StepsRunsSwitch). */
  section: StepsRunsSection
  runs: RunsView
  onRuns: (view: RunsView) => void
  /** A run was started: the page moves to its Runs. */
  onBack?: () => void
  /** Opens one run on the Runs tab (from a webhook's recent calls on the Starts tab). */
  onOpenRun?: (runFolder: string) => void
}) {
  const [error, setError] = useState('')
  const workspace = useWorkspaceState(item.workspace_path, null, false)
  const uses = useCrewWorkflowUses(item.workspace_path)
  // A run may have started while this workflow page was closed. Do not rely on its mount cache.
  useEffect(() => { if (runs.run) void workspace.refresh() }, [runs.run, workspace.refresh])
  const folders = workspace.state?.run_folders ?? []
  // The steps of the latest run, in the order they started: the dots on the Steps page.
  // A run that is in progress counts as the latest even before it has recorded its first step.
  const newestFirst = (a: { metadata?: { created_at?: string } }, b: { metadata?: { created_at?: string } }) => (b.metadata?.created_at ?? '').localeCompare(a.metadata?.created_at ?? '')
  // Follow the runs: look often while one runs or was just started, and keep how it ended on screen.
  const { running: runningRun, starting, result, dismiss, watch } = useRunWatch(folders, workspace.refresh)
  const shownRun = runningRun ?? (result ? folders.find(folder => folder.name === result) : undefined)
  const latestRun = useMemo(() => shownRun ?? [...folders].filter(folder => folder.metadata?.python_steps).sort(newestFirst)[0], [folders, shownRun])
  const latestSteps: RunPythonStep[] = Object.values(latestRun?.metadata?.python_steps ?? {}).sort((a, b) => a.started_at.localeCompare(b.started_at))
  const taskNames = useMemo(() => workflowTaskNames(item.inputs), [item.inputs])
  const runningTask = runningRun ? runTaskName(taskNames, runningRun.metadata) : undefined
  const runningStep = runningRun ? latestSteps.find(step => step.status === 'running') : undefined
  const taskDescriptions = useMemo(() => workflowTaskDescriptions(item.inputs), [item.inputs])
  useLiveRefetch(
    () => {
      void workspace.refresh()
    },
    { kinds: ['sessions', 'plan'], workflow: item.workspace_path, fallbackMs: 15_000, minIntervalMs: 1000 },
  )
  // Starts a run of one task with its saved setting (named after it), or with the task input when it has none.
  async function runTask(task: string) {
    const hasPreset = item.input_presets?.some(preset => preset.name === task)
    try {
      await callExternal('runs', { action: 'start', workflow_id: item.id, ...(hasPreset ? { input_preset: task } : { inputs: { task } }) })
    } catch (cause) {
      const data: unknown = isAxiosError(cause) ? cause.response?.data : undefined
      const message = typeof data === 'string' ? data : (data as { error?: { message?: string } } | undefined)?.error?.message
      throw new Error(message || (cause instanceof Error ? cause.message : 'Could not start this task. Try again.'))
    }
    watch()
    void workspace.refresh()
  }
  // A workflow's file opens in the project's Files viewer, the same one as every other file (#4151).
  const openFile = (file: string) => requestOpenProjectFile({ path: `${item.workspace_path}/code/${file}` }, true)
  async function ask(task: string) {
    setError('')
    try {
      await openWorkspacePaneChatWithDraft({
        tabId: chatTabId,
        message: `${task}\n\nWorkflow: ${item.manifest.label}`,
      })
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : 'Open your project chat to continue.')
    }
  }
  return (
    <div className="space-y-4">
      {(error || workspace.error) && (
        <p role="alert" className="p-3 text-sm text-destructive">
          {error || workspace.error}
        </p>
      )}
      {(starting || runningRun || shownRun) && <CrewRunBanner state={runningRun ? 'running' : starting ? 'starting' : 'ended'} run={shownRun} task={shownRun ? runTaskName(taskNames, shownRun.metadata) : undefined} steps={latestSteps} onDismiss={runningRun ? undefined : dismiss} />}
      <Suspense fallback={<p className="p-4 text-sm text-muted-foreground">Loading…</p>}>
        {section === 'steps' ? (
          (
            <div className="space-y-4">
              {taskNames.length > 0 && uses && <PageCard title={item.manifest.label} hint={uses.place} testId="crew-workflow-uses-card"><CrewWorkflowUsesBlock uses={uses} onAsk={ask} /></PageCard>}
              {taskNames.length > 0
                ? <CrewWorkflowTasks tasks={taskNames} descriptions={taskDescriptions} workspacePath={item.workspace_path} files={item.files} onOpenFile={openFile} runActive={!!runningRun} runningTask={runningTask} latestSteps={latestSteps}
                  onAskTask={task => ask(`Change the task “${task}” in this workflow. I want: `)}
                  onRunTask={runTask}
                  onDeleteTask={task => ask(`Delete the task “${task}” from this workflow. Remove it from the task input and its saved setting, delete its file (${taskFileName(task)}), and take its branch out of main.py. Tell me what you will remove and wait for my OK before deleting anything.`)} />
                : <CrewWorkflowSteps workspacePath={item.workspace_path} title={item.manifest.label} latestSteps={latestSteps} latestEvidence={latestRun?.metadata?.python_evidence ?? []} uses={uses} onCode={() => openFile('main.py')} onAsk={ask} />}
            </div>
          )
        ) : section === 'starts' ? (
          <CrewWorkflowStarts workspacePath={item.workspace_path} workflowName={item.manifest.label} onAsk={ask} onOpenRun={folder => onOpenRun?.(folder)} />
        ) : section === 'files' ? (
          <CrewWorkflowFiles workspacePath={item.workspace_path} tasks={taskNames} descriptions={taskDescriptions} onOpenFile={openFile} />
        ) : (
          <CrewWorkflowRuns workflowId={item.id} workspacePath={item.workspace_path} script tasks={taskNames} view={runs} onView={onRuns} folders={folders} loading={workspace.loading || !workspace.state} onRefresh={workspace.refresh} onAsk={ask} />
        )}
      </Suspense>
    </div>
  )
}
