import { lazy, Suspense, useCallback, useEffect, useMemo, useState } from 'react'
import { ArrowLeft, RefreshCw } from 'lucide-react'
import { agentApi } from '../../services/api'
import type { ExecutionInspectionTarget, WorkspaceStepInspection } from '../../services/api-types'
import type { WorkspaceViewId } from '../../components/workflow/workspaceViews'
import type { EmbeddedWorkflowScope } from '../../components/workflow/canvas/workspaceViewData'
import { useWorkspaceState } from '../../components/workflow/hooks/useWorkspaceState'
import { useLiveRefetch } from '../../hooks/useLiveRefetch'
import { openWorkspacePaneChatWithDraft } from '../../utils/workspacePaneChat'

const Canvas = lazy(() => import('../../components/workflow/canvas/WorkspaceViewHost'))
const Logs = lazy(() => import('../../components/workflow/ExecutionLogsPopup'))
const RelayLogs = lazy(() => import('../../components/workflow/RelayExecutionLogsView'))
const Costs = lazy(() => import('../../components/workflow/CostsPopup'))
const Inspect = lazy(() => import('../../components/chat/StepInspectionPanel'))
const Schedules = lazy(() => import('../../components/scheduler/WorkflowScheduleRunsPanel'))
const Functions = lazy(() => import('../../components/workflow/WorkflowFunctionsView'))
const Webhooks = lazy(() => import('../../components/workflow/WorkflowAPITriggersView'))

export type CrewExecutable = {
  id: string
  kind: 'workflow' | 'relay'
  workspace_path: string
  manifest: { label: string }
}
const button = 'rounded-md px-3 py-1.5 text-sm hover:bg-muted disabled:opacity-50'

/** The Crew keeps its chat. Only the selected definition/run changes in this pane. */
export function CrewExecutableEditor({
  item,
  crewAgents,
  chatTabId,
  onBack,
}: {
  item: CrewExecutable
  crewAgents: Array<{ id: string; name: string }>
  chatTabId: string
  onBack: () => void
}) {
  const [section, setSection] = useState<'steps' | 'runs' | 'settings'>('steps')
  const [view, setView] = useState<WorkspaceViewId>('flow')
  const [file, setFile] = useState<string | null>(null)
  const [runFolder, setRunFolder] = useState<string | null>(null)
  const [detail, setDetail] = useState<'logs' | 'inspect' | 'costs'>('logs')
  const [settings, setSettings] = useState<'schedules' | 'functions' | 'webhooks'>('schedules')
  const [error, setError] = useState('')
  const workspace = useWorkspaceState(item.workspace_path, runFolder, false)
  const folders = workspace.state?.run_folders ?? []
  const selectedRun = runFolder && folders.some((folder) => folder.name === runFolder) ? runFolder : null
  const openView = useCallback((next: WorkspaceViewId, target?: string) => {
    if (next === 'files') {
      setFile(target ?? null)
      return
    }
    if (next === 'schedules' || next === 'webhooks') {
      setSection('settings')
      setSettings(next)
      return
    }
    setView(next)
  }, [])
  const scope = useMemo<EmbeddedWorkflowScope>(
    () => ({
      crewAgents,
      view,
      selectedRunFolder: selectedRun,
      setSelectedRunFolder: setRunFolder,
      openView,
    }),
    [crewAgents, view, selectedRun, openView],
  )
  useLiveRefetch(
    () => {
      void workspace.refresh()
    },
    { kinds: ['sessions', 'plan'], workflow: item.workspace_path, fallbackMs: 15_000, minIntervalMs: 1000 },
  )
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
  return (
    <div className="flex h-full min-h-0 flex-col">
      <div className="flex shrink-0 items-center gap-2 border-b px-3 py-2">
        <button className={button} onClick={onBack} aria-label="Back to workflows">
          <ArrowLeft className="h-4 w-4" />
        </button>
        <h2 className="min-w-0 flex-1 truncate text-sm font-medium">{item.manifest.label}</h2>
        <button
          className={button}
          onClick={() =>
            void ask(
              section === 'runs'
                ? 'Run this workflow and report the result in this chat.'
                : 'Help me edit this workflow.',
            )
          }
        >
          {section === 'runs' ? 'Run in chat' : 'Edit in chat'}
        </button>
      </div>
      <nav aria-label="Workflow" className="flex shrink-0 gap-2 border-b p-2">
        {(['steps', 'runs', 'settings'] as const).map((value) => (
          <button
            key={value}
            aria-current={section === value ? 'page' : undefined}
            className={`${button} ${section === value ? 'bg-muted font-medium' : ''}`}
            onClick={() => {
              setSection(value)
              setFile(null)
            }}
          >
            {value[0].toUpperCase() + value.slice(1)}
          </button>
        ))}
      </nav>
      {(error || workspace.error) && (
        <p role="alert" className="p-3 text-sm text-destructive">
          {error || workspace.error}
        </p>
      )}
      <Suspense fallback={<p className="p-4 text-sm text-muted-foreground">Loading…</p>}>
        <div className="min-h-0 flex-1">
          {file ? (
            <ExecutableFile
              key={file}
              workspacePath={item.workspace_path}
              file={file}
              onClose={() => setFile(null)}
            />
          ) : section === 'steps' ? (
            <Canvas
              key={item.id}
              workspacePath={item.workspace_path}
              presetQueryId={item.id}
              relayMode={item.kind === 'relay'}
              embeddedPlanOnly
              hideToolbar
              scope={scope}
              onCreatePlan={() => void ask('Create the steps for this workflow.')}
            />
          ) : section === 'runs' ? (
            <div className="flex h-full min-h-0 flex-col">
              <div className="flex flex-wrap items-center gap-2 border-b p-2">
                {item.kind !== 'relay' && (
                  <label className="text-sm">
                    Run{' '}
                    <select
                      aria-label="Workflow run"
                      className="ml-2 max-w-full rounded border bg-background p-1.5"
                      value={selectedRun ?? ''}
                      onChange={(event) => setRunFolder(event.target.value || null)}
                    >
                      <option value="" disabled>
                        {workspace.loading ? 'Loading runs…' : 'Choose a run'}
                      </option>
                      {folders.map((folder) => (
                        <option key={folder.name} value={folder.name}>
                          {folder.name}
                          {folder.metadata?.status ? ` · ${folder.metadata.status}` : ''}
                        </option>
                      ))}
                    </select>
                  </label>
                )}
                <button
                  className={button}
                  aria-label="Refresh workflow runs"
                  onClick={() => void workspace.refresh()}
                >
                  <RefreshCw className="h-4 w-4" />
                </button>
                {(['logs', 'inspect', 'costs'] as const).map((value) => (
                  <button
                    key={value}
                    className={`${button} ${detail === value ? 'bg-muted' : ''}`}
                    onClick={() => setDetail(value)}
                  >
                    {value[0].toUpperCase() + value.slice(1)}
                  </button>
                ))}
              </div>
              <div className="min-h-0 flex-1">
                {item.kind === 'relay' ? (
                  <RelayLogs
                    relayID={item.id}
                    draftWorkspacePath={item.workspace_path}
                    draftSelectedRunFolder={selectedRun}
                    draftRunFolders={folders.map((folder) => folder.name)}
                    draftRunFolderInfos={folders}
                    onRefreshDraftRuns={workspace.refresh}
                    renderRunDetails={
                      detail === 'logs'
                        ? undefined
                        : (path, folder) =>
                            !folder ? (
                              <p className="p-4 text-sm text-muted-foreground">
                                Choose a run to see its results.
                              </p>
                            ) : detail === 'costs' ? (
                              <Costs
                                key={`${path}:${folder}`}
                                workspacePath={path}
                                runFolders={[folder]}
                                selectedRunFolder={folder}
                                runOnly
                              />
                            ) : (
                              <RunInspections
                                key={`${path}:${folder}`}
                                workspacePath={path}
                                runFolder={folder}
                              />
                            )
                    }
                  />
                ) : !selectedRun ? (
                  <p className="p-6 text-sm text-muted-foreground">
                    {folders.length
                      ? 'Choose a run to see its results.'
                      : 'No runs yet. Run the workflow from your Crew chat.'}
                  </p>
                ) : detail === 'costs' ? (
                  <Costs
                    key={selectedRun}
                    workspacePath={item.workspace_path}
                    runFolders={[selectedRun]}
                    selectedRunFolder={selectedRun}
                    runOnly
                  />
                ) : detail === 'inspect' ? (
                  <RunInspections
                    key={selectedRun}
                    workspacePath={item.workspace_path}
                    runFolder={selectedRun}
                  />
                ) : (
                  <Logs
                    key={selectedRun}
                    workspacePath={item.workspace_path}
                    runFolder={selectedRun}
                    runFolders={[selectedRun]}
                    runFolderInfos={folders.filter((folder) => folder.name === selectedRun)}
                    allowDefaultRunFolder={false}
                    onRefreshRunFolders={workspace.refresh}
                  />
                )}
              </div>
            </div>
          ) : (
            <div className="flex h-full min-h-0 flex-col">
              <nav aria-label="Workflow settings" className="flex shrink-0 gap-1 border-b p-2">
                {(['schedules', 'functions', 'webhooks'] as const).map((value) => (
                  <button
                    key={value}
                    className={`${button} ${settings === value ? 'bg-muted' : ''}`}
                    onClick={() => setSettings(value)}
                  >
                    {value === 'webhooks' ? 'Webhooks' : value === 'functions' ? 'Functions' : 'Schedules'}
                  </button>
                ))}
              </nav>
              <div className="min-h-0 flex-1 overflow-auto">
                {settings === 'schedules' ? (
                  <Schedules
                    embedded
                workflowScope={{ presetQueryId: item.id, workspacePath: item.workspace_path, label: item.manifest.label }}
                    onClose={onBack}
                  />
                ) : settings === 'functions' ? (
                  <Functions
                    workspacePath={item.workspace_path}
                    relayMode={item.kind === 'relay'}
                    relayWorkflowID={item.id}
                    onAsk={ask}
                  />
                ) : (
                  <Webhooks
                    workspacePath={item.workspace_path}
                    onViewRuns={() => setSection('runs')}
                    onAsk={(message) => ask(message)}
                  />
                )}
              </div>
            </div>
          )}
        </div>
      </Suspense>
    </div>
  )
}

function RunInspections({ workspacePath, runFolder }: { workspacePath: string; runFolder: string }) {
  const [records, setRecords] = useState<WorkspaceStepInspection[]>([])
  const [cursor, setCursor] = useState<string | null>(null)
  const [target, setTarget] = useState<ExecutionInspectionTarget | null>(null)
  const [error, setError] = useState('')
  const [loading, setLoading] = useState(true)
  const load = useCallback(
    async (after?: string) => {
      setLoading(true)
      setError('')
      try {
        const page = await agentApi.getWorkspaceStepInspections(workspacePath, { cursor: after })
        const matches = page.items.filter(
          (record) =>
            record.inspection.workspace_path === workspacePath && record.inspection.run_folder === runFolder,
        )
        setRecords((current) => (after ? [...current, ...matches] : matches))
        setCursor(page.next_cursor)
      } catch (cause) {
        setError(cause instanceof Error ? cause.message : 'Could not load step records.')
      } finally {
        setLoading(false)
      }
    },
    [workspacePath, runFolder],
  )
  useEffect(() => {
    void load()
  }, [load])
  if (target)
    return (
      <div className="flex h-full min-h-0 flex-col">
        <button className={`${button} self-start`} onClick={() => setTarget(null)}>
          Back to steps
        </button>
        <div className="min-h-0 flex-1">
          <Inspect target={target} />
        </div>
      </div>
    )
  return (
    <div className="h-full overflow-auto p-4">
      {error && (
        <p role="alert" className="text-sm text-destructive">
          {error}
        </p>
      )}
      <ul className="divide-y">
        {records.map((record) => (
          <li key={`${record.inspection.session_id}:${record.inspection.execution_id}`}>
            <button
              className="flex w-full items-center justify-between gap-3 py-3 text-left text-sm"
              onClick={() => setTarget(record.inspection)}
            >
              <span>{record.inspection.name || record.inspection.step_id}</span>
              <span className="text-xs text-muted-foreground">
                {record.status} · {new Date(record.inspection.started_at).toLocaleString()}
              </span>
            </button>
          </li>
        ))}
      </ul>
      {!loading && !records.length && (
        <p className="text-sm text-muted-foreground">
          {cursor
            ? 'No matching steps in these records.'
            : 'No inspectable step records were saved for this run. Logs may still be available.'}
        </p>
      )}
      <button
        className={`${button} mt-3 border`}
        disabled={loading}
        onClick={() => void load(cursor ?? undefined)}
      >
        {loading ? 'Loading…' : cursor ? 'Load more records' : 'Refresh'}
      </button>
    </div>
  )
}

function ExecutableFile({
  workspacePath,
  file,
  onClose,
}: {
  workspacePath: string
  file: string
  onClose: () => void
}) {
  const [content, setContent] = useState<string | null>(null)
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)
  const path = `${workspacePath}/${file}`
  useEffect(() => {
    let current = true
    void agentApi
      .getPlannerFileContent(path)
      .then((response) => {
        if (!response.success || !response.data) throw new Error(response.error || 'Could not open file.')
        if (current) setContent(String(response.data.content ?? ''))
      })
      .catch((cause) => {
        if (current) setError(String(cause))
      })
    return () => {
      current = false
    }
  }, [path])
  async function save() {
    if (content === null) return
    setBusy(true)
    setError('')
    try {
      const response = await agentApi.updatePlannerFile(path, content, 'Edit workflow source')
      if (!response.success) throw new Error(response.message || 'Could not save.')
      onClose()
    } catch (cause) {
      setError(String(cause))
    } finally {
      setBusy(false)
    }
  }
  return (
    <div className="flex h-full min-h-0 flex-col gap-3 p-3">
      <div className="flex items-center gap-2">
        <button className={button} onClick={onClose}>
          Back
        </button>
        <span className="min-w-0 flex-1 truncate text-sm">{file}</span>
        <button
          className={`${button} border`}
          disabled={busy || content === null}
          onClick={() => void save()}
        >
          Save
        </button>
      </div>
      {error && (
        <p role="alert" className="text-sm text-destructive">
          {error}
        </p>
      )}
      {content === null ? (
        <p>Loading…</p>
      ) : (
        <textarea
          aria-label="Workflow source"
          value={content}
          onChange={(event) => setContent(event.target.value)}
          className="min-h-0 flex-1 resize-none rounded border bg-background p-3 font-mono text-xs"
          spellCheck={false}
        />
      )}
    </div>
  )
}
