import { Suspense, useEffect, useMemo, useState } from 'react'
import api from '../../services/api'
import { CrewWorkflowRuns, type RunsView } from './CrewWorkflowRuns'
import { CrewFileView } from './CrewFileView'
import { CrewWorkflowSteps } from './CrewWorkflowSteps'
import { CrewWorkflowTasks, taskFileName } from './CrewWorkflowTasks'
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
  manifest: { label: string }
  /** What a person can change before a run, and the saved settings that fill it in. */
  inputs?: Array<{ name: string; value: string; description?: string; type?: string; required?: boolean; schema?: { enum?: unknown[] } }>
  input_presets?: Array<{ name: string; values: Record<string, string> }>
  files?: string[]
}

/**
 * A Crew has one workflow, and it can hold several tasks that a `task` input picks between.
 * Returns those task names (the values the `task` input allows), or an empty list when it has none.
 */
export const workflowTaskNames = (inputs: CrewExecutable['inputs']): string[] => {
  const allowed = inputs?.find(input => input.name === 'task')?.schema?.enum
  return Array.isArray(allowed) ? allowed.filter((name): name is string => typeof name === 'string' && name !== '') : []
}

/** The Crew keeps its chat. Only the selected definition/run changes in this pane. */
export function CrewExecutableEditor({
  item,
  chatTabId,
  section,
  file,
  onFile,
  runs,
  onRuns,
  onBack,
}: {
  item: CrewExecutable
  chatTabId: string
  /** The page owns the Tasks | Runs switch (StepsRunsSwitch) and which file is open, so its back arrow can close the file first. */
  section: 'steps' | 'runs'
  file: string | null
  onFile: (file: string | null) => void
  runs: RunsView
  onRuns: (view: RunsView) => void
  /** A run was started: the page moves to its Runs. */
  onBack?: () => void
}) {
  const [error, setError] = useState('')
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
  const taskNames = useMemo(() => workflowTaskNames(item.inputs), [item.inputs])
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
    void workspace.refresh()
  }
  async function ask(task: string) {
    setError('')
    try {
      await openWorkspacePaneChatWithDraft({
        tabId: chatTabId,
        message: `${task}\n\nWorkflow: ${item.manifest.label}`,
      })
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : 'Open your Crew chat to continue.')
    }
  }
  return (
    <div className="space-y-4">
      {(error || workspace.error) && (
        <p role="alert" className="p-3 text-sm text-destructive">
          {error || workspace.error}
        </p>
      )}
      <Suspense fallback={<p className="p-4 text-sm text-muted-foreground">Loading…</p>}>
        {file ? (
          <CrewFileView key={file} workspacePath={item.workspace_path} file={file} />
        ) : section === 'steps' ? (
          (
            <div className="space-y-4">
              {taskNames.length > 0
                ? <CrewWorkflowTasks tasks={taskNames} latestSteps={latestSteps}
                  onAskTask={task => ask(`Change the task “${task}” in this workflow. I want: `)}
                  onRunTask={runTask}
                  onDeleteTask={task => ask(`Delete the task “${task}” from this workflow. Remove it from the task input and its saved setting, delete its file (${taskFileName(task)}), and take its branch out of main.py. Tell me what you will remove and wait for my OK before deleting anything.`)} />
                : <CrewWorkflowSteps workspacePath={item.workspace_path} title={item.manifest.label} latestSteps={latestSteps} latestEvidence={latestRun?.metadata?.python_evidence ?? []} onCode={() => onFile('main.py')} onAsk={ask} />}
            </div>
          )
        ) : (
          <CrewWorkflowRuns workflowId={item.id} workspacePath={item.workspace_path} script tasks={taskNames} view={runs} onView={onRuns} folders={folders} loading={workspace.loading || !workspace.state} onRefresh={workspace.refresh} onAsk={ask} />
        )}
      </Suspense>
    </div>
  )
}
