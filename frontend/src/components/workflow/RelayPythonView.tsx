import { useCallback, useEffect, useRef, useState } from 'react'
import { ArrowDown, Code2, RefreshCw, Settings2, Sparkles } from 'lucide-react'
import { agentApi } from '../../services/api'
import type { RunFolderInfo } from '../../services/api-types'
import { useWorkflowStore } from '../../stores/useWorkflowStore'
import { useLiveRefetch } from '../../hooks/useLiveRefetch'
import { VariablesSidebar } from './canvas/VariablesSidebar'
import { MarkdownRenderer } from '../ui/MarkdownRenderer'
import { workflowWebhooksApi, type RelayReleasesResponse } from '../../api/workflowWebhooks'

interface RelayToolCall {
  name: string
  args?: unknown
  result?: unknown
  error?: string
}

interface RelayAgentCall {
  id: string
  name: string
  status: string
  provider?: string
  model?: string | { provider?: string; model_id?: string }
  started_at?: string | number
  completed_at?: string | number
  output?: unknown
  error?: string
  tools?: RelayToolCall[]
}

interface RelayTrace {
  version: number
  status: string
  error?: string
  calls: RelayAgentCall[]
}

function missingFile(error: unknown): boolean {
  return (error as { response?: { status?: number } })?.response?.status === 404
}

async function readFile(path: string): Promise<unknown> {
  const response = await agentApi.getPlannerFileContent(path)
  if (!response.success || !response.data) throw new Error(response.error || 'Could not read Relay file')
  return response.data.content
}

function JSONValue({ value }: { value: unknown }) {
  return <pre className="max-h-80 overflow-auto whitespace-pre-wrap break-words rounded-md bg-muted/40 p-3 font-mono text-xs">{JSON.stringify(value, null, 2)}</pre>
}

/** Builder-authored overview for people; exact code and recorded runs remain inspectable. */
export default function RelayPythonView({ workspacePath, relayID, onBuild }: {
  workspacePath: string | null
  relayID?: string | null
  onBuild: () => void
}) {
  const [tab, setTab] = useState<'overview' | 'calls' | 'source'>('overview')
  const [overview, setOverview] = useState<string | null>(null)
  const [overviewError, setOverviewError] = useState('')
  const [source, setSource] = useState<string | null>(null)
  const [sourceError, setSourceError] = useState('')
  const [runs, setRuns] = useState<RunFolderInfo[]>([])
  const [runError, setRunError] = useState('')
  const [trace, setTrace] = useState<RelayTrace | null>(null)
  const [result, setResult] = useState<unknown>(undefined)
  const [traceError, setTraceError] = useState('')
  const [loadingSource, setLoadingSource] = useState(true)
  const [loadingTrace, setLoadingTrace] = useState(false)
  const [configurationOpen, setConfigurationOpen] = useState(false)
  const [releases, setReleases] = useState<RelayReleasesResponse | null>(null)
  const [version, setVersion] = useState('draft')
  const [releaseError, setReleaseError] = useState('')
  const selectedRunFolder = useWorkflowStore(state => state.selectedRunFolder)
  const refreshToken = useWorkflowStore(state => state.workspaceViewRefreshToken)
  const folder = runs.some(run => run.name === selectedRunFolder) ? selectedRunFolder : runs[0]?.name
  const sourceRequest = useRef(0)
  const traceRequest = useRef(0)
  const runsRequest = useRef(0)
  const releaseRequest = useRef(0)
  const selectedRelease = releases?.releases.find(release => release.version === version)
  const runWorkspace = version === 'draft' ? workspacePath : selectedRelease?.workspace_path

  const refreshSource = useCallback(async () => {
    if (!workspacePath) return
    const request = ++sourceRequest.current
    const [sourceResponse, overviewResponse] = await Promise.allSettled([
      readFile(`${workspacePath}/relay.py`),
      readFile(`${workspacePath}/relay.md`),
    ])
    if (request !== sourceRequest.current) return
    if (sourceResponse.status === 'fulfilled') {
      setSource(String(sourceResponse.value ?? ''))
      setSourceError('')
    } else {
      setSource(null)
      setSourceError(missingFile(sourceResponse.reason) ? '' : sourceResponse.reason instanceof Error ? sourceResponse.reason.message : 'Could not load relay.py')
    }
    if (overviewResponse.status === 'fulfilled') {
      setOverview(String(overviewResponse.value ?? '').trim() || null)
      setOverviewError('')
    } else {
      setOverview(null)
      setOverviewError(missingFile(overviewResponse.reason) ? '' : 'Could not load the Relay overview. Try refreshing.')
    }
    setLoadingSource(false)
  }, [workspacePath])

  const refreshRuns = useCallback(async () => {
    if (!runWorkspace) return
    const request = ++runsRequest.current
    try {
      const response = await agentApi.getRunFolders(runWorkspace)
      if (request !== runsRequest.current) return
      setRuns(response.folders ?? [])
      setRunError('')
    } catch (error) {
      if (request === runsRequest.current) setRunError(error instanceof Error ? error.message : 'Could not load Relay runs')
    }
  }, [runWorkspace])

  const refreshTrace = useCallback(async () => {
    if (!runWorkspace || !folder) return
    const request = ++traceRequest.current
    const [traceResponse, resultResponse] = await Promise.allSettled([
      readFile(`${runWorkspace}/runs/${folder}/relay_trace.json`),
      readFile(`${runWorkspace}/runs/${folder}/relay_result.json`),
    ])
    if (request !== traceRequest.current) return
    try {
      if (traceResponse.status === 'rejected') throw traceResponse.reason
      const parsed = typeof traceResponse.value === 'string' ? JSON.parse(traceResponse.value) : traceResponse.value
      if (!parsed || parsed.version !== 1 || !Array.isArray(parsed.calls)) throw new Error('This run has no supported Python Relay trace')
      setTrace(parsed)
      setTraceError('')
    } catch (error) {
      setTrace(null)
      setTraceError(missingFile(error) ? 'No recorded steps yet for this run.' : error instanceof Error ? error.message : 'Could not load recorded calls')
    }
    if (resultResponse.status === 'fulfilled') {
      try {
        setResult(typeof resultResponse.value === 'string' ? JSON.parse(resultResponse.value) : resultResponse.value)
      } catch {
        setResult(undefined)
        setTraceError('The saved Relay result is not valid JSON.')
      }
    } else {
      setResult(undefined)
      if (!missingFile(resultResponse.reason)) setTraceError('Could not read the saved Relay result.')
    }
    setLoadingTrace(false)
  }, [runWorkspace, folder])

  const refreshReleases = useCallback(async (selectActive = false) => {
    if (!relayID) return
    const request = ++releaseRequest.current
    try {
      const response = await workflowWebhooksApi.relayReleases(relayID)
      if (request !== releaseRequest.current) return
      setReleases(response)
      setReleaseError('')
      if (selectActive) setVersion(response.active_version && response.releases.some(release => release.version === response.active_version) ? response.active_version : 'draft')
    } catch (error) {
      if (request === releaseRequest.current) setReleaseError(error instanceof Error ? error.message : 'Could not load published versions')
    }
  }, [relayID])

  useEffect(() => {
    setReleases(null); setVersion('draft'); setReleaseError('')
    void refreshReleases(true)
    return () => { releaseRequest.current++ }
  }, [refreshReleases])

  useEffect(() => {
    setRuns([]); setTrace(null); setResult(undefined); setTraceError(''); setRunError('')
    void refreshRuns()
    return () => { runsRequest.current++; traceRequest.current++ }
  }, [refreshRuns])

  useEffect(() => {
    setSource(null); setOverview(null); setOverviewError(''); setTab('overview')
    setRuns([]); setTrace(null); setResult(undefined)
    setSourceError(''); setRunError(''); setTraceError(''); setLoadingSource(true)
    setConfigurationOpen(false)
    void refreshSource()
    return () => { sourceRequest.current++; traceRequest.current++ }
  }, [refreshSource])

  useEffect(() => {
    setTrace(null); setResult(undefined); setTraceError('')
    setLoadingTrace(!!folder)
    void refreshTrace()
    return () => { traceRequest.current++ }
  }, [refreshTrace, folder, refreshToken])

  const refresh = useCallback(() => { void refreshSource(); void refreshRuns(); void refreshTrace() }, [refreshSource, refreshRuns, refreshTrace])
  useLiveRefetch(refresh, { kinds: ['plan', 'sessions', 'schedules'], workflow: workspacePath, fallbackMs: 3000, safetyMs: tab === 'calls' || trace?.status === 'running' ? 2000 : 30_000, minIntervalMs: 500 })

  return <div className="relative flex h-full min-h-0 flex-col bg-background">
    <div className="flex flex-wrap items-center gap-2 border-b border-border px-3 py-2">
      <div className="flex gap-1" role="tablist" aria-label="Relay">
        {(['overview', 'calls', 'source'] as const).map(value => <button key={value} type="button" role="tab" aria-selected={tab === value} aria-controls={`relay-python-${value}`} onClick={() => setTab(value)} className={`rounded-md px-3 py-1.5 text-xs font-medium ${tab === value ? 'bg-muted text-foreground' : 'text-muted-foreground hover:bg-muted/60'}`}>{value === 'overview' ? 'Overview' : value === 'calls' ? 'Runs' : 'Code'}</button>)}
      </div>
      <div className="ml-auto flex items-center gap-1">
        <button type="button" onClick={() => setConfigurationOpen(open => !open)} aria-label="Relay configuration" className="rounded-md p-1.5 text-muted-foreground hover:bg-muted"><Settings2 className="h-4 w-4" /></button>
        <button type="button" onClick={() => { refresh(); void refreshReleases() }} aria-label="Refresh Relay" className="rounded-md p-1.5 text-muted-foreground hover:bg-muted"><RefreshCw className="h-4 w-4" /></button>
      </div>
    </div>
    {tab === 'overview' ? <div id="relay-python-overview" role="tabpanel" className="min-h-0 flex-1 overflow-auto">
      <div className="flex flex-wrap items-center justify-between gap-3 border-b border-border px-4 py-3">
        <div><h2 className="text-sm font-semibold">Your Relay</h2><p className="mt-1 text-xs text-muted-foreground">Draft overview · Describe changes in the builder chat.</p></div>
        <button type="button" onClick={onBuild} className="inline-flex items-center gap-1.5 rounded-md border border-border px-3 py-1.5 text-xs hover:bg-muted"><Sparkles className="h-3.5 w-3.5" />Edit in chat</button>
      </div>
      {loadingSource ? <p className="p-6 text-sm text-muted-foreground">Loading overview…</p> : overviewError ? <p role="alert" className="p-6 text-sm text-destructive">{overviewError}</p> : overview ? <div className="mx-auto max-w-3xl p-6"><MarkdownRenderer content={overview} basePath={workspacePath ?? undefined} /></div> : <div className="mx-auto max-w-xl p-6">
        <h3 className="text-lg font-semibold">Build an agent in chat</h3>
        <p className="mt-2 text-sm leading-6 text-muted-foreground">Describe what you want to automate. The builder creates your Relay and explains how it works here.</p>
        <div className="mt-6 space-y-4 text-sm">
          <section><h4 className="font-medium">What goes in?</h4><p className="mt-1 text-muted-foreground">The information your website or app will send, such as an invoice or a support request.</p></section>
          <section><h4 className="font-medium">What should happen?</h4><p className="mt-1 text-muted-foreground">One agent or several steps, with the tools and decisions you need.</p></section>
          <section><h4 className="font-medium">What comes back?</h4><p className="mt-1 text-muted-foreground">The result your app should receive, such as extracted invoice fields.</p></section>
        </div>
        {source !== null && <p className="mt-6 text-xs text-muted-foreground">This Relay has an implementation but no overview yet. Ask the builder to explain it.</p>}
        <button type="button" onClick={onBuild} className="mt-5 rounded-md bg-primary px-3 py-2 text-sm text-primary-foreground">Describe it in chat</button>
      </div>}
    </div> : tab === 'source' ? <div id="relay-python-source" role="tabpanel" className="flex min-h-0 flex-1 flex-col">
      <div className="flex items-center justify-between gap-3 border-b border-border px-4 py-2 text-xs text-muted-foreground"><span>Advanced · Implementation managed by the builder.</span><button type="button" onClick={() => useWorkflowStore.getState().openWorkspaceView('files', 'relay.py')} className="inline-flex items-center gap-1 rounded px-2 py-1 hover:bg-muted hover:text-foreground"><Code2 className="h-3.5 w-3.5" />Open in Files</button></div>
      {loadingSource ? <p className="p-6 text-sm text-muted-foreground">Loading source…</p> : sourceError ? <p role="alert" className="p-6 text-sm text-destructive">{sourceError}</p> : source === null ? <div className="m-auto max-w-md p-6 text-center"><h2 className="font-semibold">Build your Relay</h2><p className="mt-2 text-sm text-muted-foreground">Describe what your Relay should do in chat. The builder creates its implementation for you.</p><button type="button" onClick={onBuild} className="mt-4 rounded-md bg-primary px-3 py-2 text-sm text-primary-foreground">Build in chat</button></div> : <pre className="min-h-0 flex-1 overflow-auto p-4 font-mono text-xs leading-6" aria-label="Relay Python source">{source}</pre>}
    </div> : <div id="relay-python-calls" role="tabpanel" className="min-h-0 flex-1 overflow-auto p-4">
      <div className="mb-4 flex flex-wrap items-center gap-2 text-xs"><label htmlFor="relay-python-version">Version</label><select id="relay-python-version" value={version} onChange={event => setVersion(event.target.value)} className="rounded-md border border-border bg-background px-2 py-1.5"><option value="draft">Draft tests</option>{releases?.releases.map(release => <option key={release.version} value={release.version}>{release.version}{release.version === releases.active_version ? ' (active)' : ''}</option>)}</select><label htmlFor="relay-python-run">Run</label><select id="relay-python-run" value={folder ?? ''} onChange={event => useWorkflowStore.getState().setSelectedRunFolder(event.target.value || null)} className="max-w-full rounded-md border border-border bg-background px-2 py-1.5"><option value="" disabled>{runs.length ? 'Select a run' : 'No runs yet'}</option>{runs.map(run => <option key={run.name} value={run.name}>{run.name}{run.metadata?.status ? ` · ${run.metadata.status}` : ''}</option>)}</select>{trace && <span role="status" className="rounded bg-muted px-2 py-1">{trace.status}</span>}</div>
      {(releaseError || selectedRelease?.error || releases?.active_error) && <p role="alert" className="mb-3 text-sm text-destructive">{releaseError || selectedRelease?.error || releases?.active_error}</p>}
      <p className="mb-4 text-xs text-muted-foreground">Steps performed in this run. Expand a step to inspect its result and tool activity.</p>
      {runError && <p role="alert" className="mb-3 text-sm text-destructive">{runError}</p>}
      {loadingTrace ? <p className="text-sm text-muted-foreground">Loading run…</p> : traceError ? <p role="alert" className="text-sm text-muted-foreground">{traceError}</p> : !folder ? <p className="text-sm text-muted-foreground">Ask the Builder to test your Relay with sample JSON. Its steps and final result will appear here.</p> : null}
      {trace?.error && <p role="alert" className="mb-3 text-sm text-destructive">{trace.error}</p>}
      {trace?.calls.map((call, index) => <div key={call.id || index}>
        {index > 0 && <ArrowDown aria-hidden="true" className="mx-auto my-2 h-4 w-4 text-muted-foreground" />}
        <article className="rounded-lg border border-border p-3"><header className="flex flex-wrap items-center justify-between gap-2 text-sm"><strong>{call.name || call.id}</strong><span className="text-xs text-muted-foreground">{call.status}</span></header>{(call.provider || call.model) && <p className="mt-1 text-xs text-muted-foreground">{[call.provider, typeof call.model === 'object' ? [call.model?.provider, call.model?.model_id].filter(Boolean).join(':') : call.model].filter(Boolean).join(' · ')}</p>}{call.error && <p role="alert" className="mt-2 text-xs text-destructive">{call.error}</p>}{call.output !== undefined && <details className="mt-3 text-xs"><summary className="cursor-pointer font-medium">Output</summary><JSONValue value={call.output} /></details>}{call.tools?.map((tool, toolIndex) => <details key={`${tool.name}-${toolIndex}`} className="mt-2 text-xs"><summary className="cursor-pointer">Tool: {tool.name}</summary>{tool.args !== undefined && <JSONValue value={tool.args} />}{tool.result !== undefined && <JSONValue value={tool.result} />}{tool.error && <p className="text-destructive">{tool.error}</p>}</details>)}</article>
      </div>)}
      {trace && trace.calls.length === 0 && <p className="text-sm text-muted-foreground">No agent steps recorded in this run.</p>}
      {result !== undefined && <section className="mt-5"><h3 className="mb-2 text-sm font-semibold">Final result</h3><JSONValue value={result} /></section>}
    </div>}
    {configurationOpen && <VariablesSidebar workspacePath={workspacePath} relayMode onClose={() => setConfigurationOpen(false)} />}
  </div>
}
