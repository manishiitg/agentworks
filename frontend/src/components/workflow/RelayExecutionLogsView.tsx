import { useCallback, useEffect, useMemo, useState } from 'react'
import { workflowWebhooksApi, type RelayReleasesResponse } from '../../api/workflowWebhooks'
import { agentApi } from '../../services/api'
import type { RunFolderInfo } from '../../services/api-types'
import ExecutionLogsPopup from './ExecutionLogsPopup'
import DBOSExecutionTimeline from './DBOSExecutionTimeline'

/** Selects a Relay release, then uses the shared workflow execution log viewer. */
export default function RelayExecutionLogsView({ relayID, draftWorkspacePath, draftRunFolders, draftRunFolderInfos, draftSelectedRunFolder, onRefreshDraftRuns }: {
  relayID: string
  draftWorkspacePath: string
  draftRunFolders: string[]
  draftRunFolderInfos: RunFolderInfo[]
  draftSelectedRunFolder: string | null
  onRefreshDraftRuns: () => void | Promise<void>
}) {
  const [releases, setReleases] = useState<RelayReleasesResponse | null>(null)
  const [selectedVersion, setSelectedVersion] = useState<string | null>(null)
  const [publishedRuns, setPublishedRuns] = useState<RunFolderInfo[]>([])
  const [error, setError] = useState('')
  const [selectedRun, setSelectedRun] = useState<string | null>(null)
  const [mode, setMode] = useState<'steps' | 'files'>('steps')
  const [hasDBOS, setHasDBOS] = useState<boolean | null>(null)
  const onAvailable = useCallback((available: boolean) => { setHasDBOS(available); if (!available) setMode('files') }, [])

  useEffect(() => { setSelectedVersion(null); setPublishedRuns([]); setReleases(null) }, [relayID])

  const refreshReleases = useCallback(async () => {
    try {
      const next = await workflowWebhooksApi.relayReleases(relayID)
      setReleases(next)
      setSelectedVersion(previous => previous === null ? (next.active_version || 'draft') : previous)
      setError('')
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : 'Could not load Relay releases')
    }
  }, [relayID])
  useEffect(() => { void refreshReleases() }, [refreshReleases])

  const selectedRelease = useMemo(
    () => releases?.releases.find(release => release.version === selectedVersion),
    [releases, selectedVersion],
  )
  const workspacePath = selectedRelease?.workspace_path || draftWorkspacePath
  const refreshPublishedRuns = useCallback(async () => {
    if (!selectedRelease) return
    try {
      const response = await agentApi.getRunFolders(selectedRelease.workspace_path)
      setPublishedRuns(response.folders || [])
      setError('')
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : 'Could not load published runs')
    }
  }, [selectedRelease])
  useEffect(() => {
    setPublishedRuns([])
    void refreshPublishedRuns()
  }, [refreshPublishedRuns])

  const runFolderInfos = selectedRelease ? publishedRuns : draftRunFolderInfos
  const runFolders = selectedRelease ? publishedRuns.map(run => run.name) : draftRunFolders
  const folder = selectedRun && runFolders.includes(selectedRun) ? selectedRun : runFolders[0] || null
  useEffect(() => { setSelectedRun(null); setHasDBOS(null); setMode('steps') }, [workspacePath])
  return <div className="flex h-full min-h-0 flex-col">
    <div className="flex flex-wrap items-center gap-2 border-b border-border px-3 py-2 text-xs">
      <label htmlFor="relay-log-version" className="font-medium">Run version</label>
      <select
        id="relay-log-version"
        className="rounded-md border border-border bg-background px-2 py-1"
        value={selectedVersion || 'draft'}
        onChange={event => setSelectedVersion(event.target.value)}
      >
        <option value="draft">Draft tests</option>
        {releases?.releases.map(release => <option key={release.version} value={release.version}>
          {release.version}{release.error ? ' (unavailable)' : ''}{release.version === releases.active_version ? ' (active)' : ''}
        </option>)}
      </select>
      {selectedRelease?.error && <span role="alert" className="text-destructive">{selectedRelease.error}</span>}
      {releases?.active_error && <span role="alert" className="text-destructive">{releases.active_error}</span>}
      {error && <span role="alert" className="text-destructive">{error}</span>}
      <label htmlFor="relay-log-run">Run</label><select id="relay-log-run" value={folder || ''} onChange={event => { setSelectedRun(event.target.value); setHasDBOS(null); setMode('steps') }} className="rounded-md border border-border bg-background px-2 py-1"><option value="" disabled>No runs yet</option>{runFolders.map(run => <option key={run} value={run}>{run}</option>)}</select>
      <button type="button" disabled={hasDBOS === false} aria-pressed={mode === 'steps'} onClick={() => setMode('steps')} className="rounded border border-border px-2 py-1 disabled:opacity-40">DBOS steps</button>
      <button type="button" aria-pressed={mode === 'files'} onClick={() => setMode('files')} className="rounded border border-border px-2 py-1">Agent files</button>
    </div>
    <div className={mode === 'steps' ? 'min-h-0 flex-1 overflow-auto' : 'hidden'}>
      <DBOSExecutionTimeline key={`${workspacePath}/${folder}`} workspacePath={workspacePath} runFolder={folder} onAvailable={onAvailable} />
    </div>
    <div className={mode === 'files' ? 'min-h-0 flex-1' : 'hidden'}>
      <ExecutionLogsPopup
        key={`${workspacePath}/${folder}`}
        workspacePath={workspacePath}
        runFolder={folder || (selectedRelease ? null : draftSelectedRunFolder)}
        runFolders={runFolders}
        runFolderInfos={runFolderInfos}
        onRefreshRunFolders={selectedRelease ? refreshPublishedRuns : onRefreshDraftRuns}
      />
    </div>
  </div>
}
