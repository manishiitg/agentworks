import { lazy, Suspense, useCallback, useEffect, useMemo, useState } from 'react'
import { ArrowLeft } from 'lucide-react'
import api, { agentApi } from '../../services/api'
import { CrewWorkflowRuns } from './CrewWorkflowRuns'
import { PageCard, RowButton } from '../../components/automation/AutomationKit'
import type { WorkspaceViewId } from '../../components/workflow/workspaceViews'
import type { EmbeddedWorkflowScope } from '../../components/workflow/canvas/workspaceViewData'
import { useWorkspaceState } from '../../components/workflow/hooks/useWorkspaceState'
import { useLiveRefetch } from '../../hooks/useLiveRefetch'
import { useWorkflowStore } from '../../stores/useWorkflowStore'
import { openWorkspacePaneChatWithDraft } from '../../utils/workspacePaneChat'

const Canvas = lazy(() => import('../../components/workflow/canvas/WorkspaceViewHost'))

export type CrewExecutable = {
  id: string
  kind: 'workflow' | 'script'
  workspace_path: string
  manifest: { label: string }
  used_by?: Array<{ workflow: string; step: string }>
}
const button = 'rounded-md px-3 py-1.5 text-sm hover:bg-muted disabled:opacity-50'

/** The Crew keeps its chat. Only the selected definition/run changes in this pane. */
export function CrewExecutableEditor({
  item,
  crewAgents,
  crewScripts = [],
  chatTabId,
  section: controlledSection,
  onBack,
}: {
  item: CrewExecutable
  crewAgents: Array<{ id: string; name: string }>
  crewScripts?: Array<{ id: string; name: string }>
  chatTabId: string
  /** Set by the Workflow page, which owns the Plan | Runs switch. Without it the editor shows its own header and tabs. */
  section?: 'plan' | 'runs'
  onBack?: () => void
}) {
  const [ownSection, setSection] = useState<'plan' | 'runs'>('plan')
  const embedded = controlledSection !== undefined
  const section = controlledSection ?? ownSection
  const [view, setView] = useState<WorkspaceViewId>('flow')
  const [file, setFile] = useState<string | null>(null)
  const [runFolder, setRunFolder] = useState<string | null>(null)
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
      useWorkflowStore.getState().openWorkspaceView('workshop', next === 'webhooks' ? 'triggers' : 'schedules')
      return
    }
    setView(next)
  }, [])
  const scope = useMemo<EmbeddedWorkflowScope>(
    () => ({
      crewAgents,
      crewScripts,
      onEditInputs: () => { void ask('Help me edit the inputs for this workflow.') },
      view,
      selectedRunFolder: selectedRun,
      setSelectedRunFolder: setRunFolder,
      openView,
    }),
    [crewAgents, crewScripts, view, selectedRun, openView, chatTabId, item],
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
    <div className={embedded ? 'space-y-4' : 'flex h-full min-h-0 flex-col'}>
      {!embedded && <div className="flex shrink-0 items-center gap-2 border-b px-3 py-2">
        {onBack && <button className={button} onClick={onBack} aria-label={item.kind === 'script' ? 'Back to scripts' : 'Back to workflows'}>
          <ArrowLeft className="h-4 w-4" />
        </button>}
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
      </div>}
      {item.kind === 'script' && !!item.used_by?.length && <p className="border-b px-3 py-2 text-xs text-muted-foreground">Used by {Array.from(new Set(item.used_by.map(use => use.workflow))).join(', ')}. Changes apply to the next run.</p>}
      {!embedded && <nav aria-label="Workflow" className="flex shrink-0 gap-2 border-b p-2">
        {(['plan', 'runs'] as const).map((value) => (
          <button
            key={value}
            aria-current={section === value ? 'page' : undefined}
            className={`${button} ${section === value ? 'bg-muted font-medium' : ''}`}
            onClick={() => {
              setSection(value)
              setFile(null)
            }}
          >
            {value === 'plan' ? (item.kind === 'script' ? 'Code' : 'Plan') : 'Runs'}
          </button>
        ))}
      </nav>}
      {(error || workspace.error) && (
        <p role="alert" className="p-3 text-sm text-destructive">
          {error || workspace.error}
        </p>
      )}
      <Suspense fallback={<p className="p-4 text-sm text-muted-foreground">Loading…</p>}>
        <div className={embedded ? '' : 'min-h-0 flex-1'}>
          {file ? (
            <div className={embedded ? 'h-[75vh] min-h-96' : 'h-full'}>
              <ExecutableFile
                key={file}
                workspacePath={item.workspace_path}
                file={file}
                onClose={() => setFile(null)}
              />
            </div>
          ) : section === 'plan' && item.kind === 'script' ? (
            <ExecutableFile workspacePath={item.workspace_path} file="main.py" crewScript onClose={() => onBack?.()} />
          ) : section === 'plan' ? (
            <PageCard
              title={item.manifest.label}
              onAsk={() => ask('Help me edit this workflow.')}
              action={<RowButton primary onClick={() => void ask('Run this workflow and report the result in this chat.')}>Run (in chat)</RowButton>}
              testId="crew-plan-card"
            >
            <div className="h-[600px]">
            <Canvas
              key={item.id}
              workspacePath={item.workspace_path}
              presetQueryId={item.id}
              embeddedPlanOnly
              readOnly
              hideToolbar
              scope={scope}
              onCreatePlan={() => void ask('Create the steps for this workflow.')}
            />
            </div>
            </PageCard>
          ) : section === 'runs' ? (
            <CrewWorkflowRuns workspacePath={item.workspace_path} folders={folders} loading={workspace.loading} onRefresh={workspace.refresh} onAsk={ask} />
          ) : null}
        </div>
      </Suspense>
    </div>
  )
}

function ExecutableFile({
  workspacePath,
  file,
  onClose,
  crewScript = false,
}: {
  workspacePath: string
  crewScript?: boolean
  file: string
  onClose: () => void
}) {
  const [content, setContent] = useState<string | null>(null)
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)
  const path = `${workspacePath}/${file}`
  useEffect(() => {
    let current = true
    void (crewScript ? api.get('/api/crew-script-file', { params: { workspace_path: workspacePath, file } }).then(response => response.data) : agentApi.getPlannerFileContent(path))
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
      const response = crewScript ? (await api.put('/api/crew-script-file', { content }, { params: { workspace_path: workspacePath, file } })).data : await agentApi.updatePlannerFile(path, content, 'Edit workflow source')
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
