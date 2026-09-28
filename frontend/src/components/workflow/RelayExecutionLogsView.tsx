import { useCallback, useEffect, useMemo, useState } from 'react'
import { workflowWebhooksApi, type RelayReleasesResponse } from '../../api/workflowWebhooks'
import { agentApi } from '../../services/api'
import type { RunFolderInfo } from '../../services/api-types'
import ExecutionLogsPopup from './ExecutionLogsPopup'

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
          {release.version}{release.version === releases.active_version ? ' (active)' : ''}
        </option>)}
      </select>
      {error && <span role="alert" className="text-destructive">{error}</span>}
    </div>
    <div className="min-h-0 flex-1">
      <ExecutionLogsPopup
        key={workspacePath}
        workspacePath={workspacePath}
        runFolder={selectedRelease ? null : draftSelectedRunFolder}
        runFolders={runFolders}
        runFolderInfos={runFolderInfos}
        onRefreshRunFolders={selectedRelease ? refreshPublishedRuns : onRefreshDraftRuns}
      />
    </div>
  </div>
}
