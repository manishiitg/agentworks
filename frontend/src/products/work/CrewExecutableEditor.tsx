import { Suspense, useEffect, useMemo, useState } from 'react'
import api, { agentApi } from '../../services/api'
import { CrewWorkflowRuns } from './CrewWorkflowRuns'
import { CrewWorkflowSteps } from './CrewWorkflowSteps'
import { PageCard, PageRow, RowButton } from '../../components/automation/AutomationKit'
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
}
const button = 'rounded-md px-3 py-1.5 text-sm hover:bg-muted disabled:opacity-50'

/** The Crew keeps its chat. Only the selected definition/run changes in this pane. */
export function CrewExecutableEditor({
  item,
  chatTabId,
  section,
  onBack,
}: {
  item: CrewExecutable
  chatTabId: string
  /** The page owns the Steps | Runs switch (StepsRunsSwitch). */
  section: 'steps' | 'runs'
  onBack?: () => void
}) {
  const [file, setFile] = useState<string | null>(null)
  // Switching Steps | Runs leaves any opened file.
  useEffect(() => { setFile(null) }, [section])
  const [error, setError] = useState('')
  const workspace = useWorkspaceState(item.workspace_path, null, false)
  const folders = workspace.state?.run_folders ?? []
  // The steps of the latest run, in the order they started: the dots on the Steps page.
  const latestSteps = useMemo<RunPythonStep[]>(() => {
    const latest = [...folders].filter(folder => folder.metadata?.python_steps).sort((a, b) => (b.metadata?.created_at ?? '').localeCompare(a.metadata?.created_at ?? ''))[0]
    return Object.values(latest?.metadata?.python_steps ?? {}).sort((a, b) => a.started_at.localeCompare(b.started_at))
  }, [folders])
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
          <div className="h-[75vh] min-h-96">
            <ExecutableFile key={file} workspacePath={item.workspace_path} file={file} crewScript={script} onClose={() => setFile(null)} />
          </div>
        ) : section === 'steps' ? (
          !script ? (
            <PageCard title={item.manifest.label} testId="crew-old-workflow-card">
              <p className="px-4 py-3 text-sm text-muted-foreground">This workflow was made in the old format. Ask your agent to convert it to a new workflow; what it does stays the same.</p>
              <div className="px-4 pb-3"><RowButton primary onClick={() => void ask('Convert this workflow to a new workflow and keep what it does.')}>Ask your agent to convert it</RowButton></div>
            </PageCard>
          ) : (
            <div className="space-y-4">
              <PageCard title={item.manifest.label} onAsk={() => ask('Help me change this workflow.')}
                action={<RowButton primary onClick={() => void ask('Run this workflow and report the result in this chat.')}>Run</RowButton>} testId="crew-workflow-card">
                <p className="px-4 py-3 text-xs text-muted-foreground">{!latestSteps.length ? 'Not run yet.' : latestSteps.some(step => step.status === 'failed') ? 'Last run: a step failed.' : latestSteps.some(step => step.status === 'running') ? 'Running now.' : 'Last run: finished.'}</p>
              </PageCard>
              <CrewWorkflowSteps workspacePath={item.workspace_path} latestSteps={latestSteps} onCode={() => setFile('main.py')} onAsk={ask}
                empty={<ExecutableFile workspacePath={item.workspace_path} file="main.py" crewScript onClose={() => onBack?.()} />} />
              <PageCard title="Files" hint="The workflow's code and what it uses." onAsk={() => ask('Help me understand the files of this workflow.')}>
                <PageRow title="main.py" lines={['The workflow']} action={<RowButton onClick={() => setFile('main.py')}>Edit</RowButton>} />
                <PageRow title="script.json" lines={['Name, inputs and saved settings']} action={<RowButton onClick={() => setFile('script.json')}>Edit</RowButton>} />
              </PageCard>
            </div>
          )
        ) : (
          <CrewWorkflowRuns workspacePath={item.workspace_path} script={script} folders={folders} loading={workspace.loading} onRefresh={workspace.refresh} onAsk={ask} />
        )}
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
