import React, { useEffect, useMemo, useRef, useState } from 'react'
import { Loader2, AlertCircle, FileText, Route as RouteIcon, Braces } from 'lucide-react'
import { WorkspacePanelBack } from '../workspace/WorkspacePanelNavigation'
import type { RunFolderInfo, StepExecutionLogs } from '../../services/api-types'
import { agentApi } from '../../services/api'
import InspectorShell from './InspectorShell'
import { LogsHeader } from './executionLogs/LogsHeader'
import { StepContent } from './executionLogs/StepContent'
import { StepList } from './executionLogs/StepList'
import { useExecutionLogsData } from './executionLogs/useExecutionLogsData'
import { formatLogFileContent, getStepStatus, isWebhookRunFolder, sortStepEntriesByExecution } from './executionLogs/helpers'

const headerRowClass = 'flex items-center justify-between gap-3 border-b border-border px-3 py-2'

interface ExecutionLogsPopupProps {
  workspacePath: string | null
  defaultRunFolder?: string | null
  initialStepId?: string
  allowDefaultRunFolder?: boolean
  runFolder: string | null
  runFolders: string[] // Available run folders (iterations and groups)
  runFolderInfos?: RunFolderInfo[] // Lifecycle timestamps for dropdown labels
  startedAt?: string | null
  // Refreshes the run_folder LIST itself (a new folder appearing after a
  // standalone execute_step run, e.g.), as opposed to the panel's own
  // refresh, which only re-fetches logs for the already-selected folder.
  // Without this, a folder that didn't exist when runFolders was last loaded
  // stays invisible in the dropdown no matter how many times the panel's own
  // refresh is clicked.
  onRefreshRunFolders?: () => void | Promise<void>
}

const ExecutionLogsPopup: React.FC<ExecutionLogsPopupProps> = ({
  workspacePath,
  initialStepId,
  defaultRunFolder,
  runFolder: initialRunFolder,
  runFolders,
  allowDefaultRunFolder = true,
  runFolderInfos = [],
  startedAt,
  onRefreshRunFolders,
}) => {
  const {
    runFolderOptions,
    loading,
    logs,
    error,
    expandedSteps,
    expandedValidations,
    expandedExecutions,
    expandedArchived,
    selectedRunFolder,
    setSelectedRunFolder,
    stepSearchQueries,
    setStepSearchQueries,
    routeFilterKey,
    setRouteFilterKey,
    routingRouteGroups,
    expandedFiles,
    fileContents,
    loadingFiles,
    focusedStepId,
    handleStepDetailScroll,
    loadLogs,
    toggleStep,
    toggleValidation,
    toggleExecution,
    toggleArchived,
    toggleFileExpansion,
  } = useExecutionLogsData({ workspacePath, initialRunFolder, runFolders, allowDefaultRunFolder, defaultRunFolder, initialStepId })

  const webhookRun = isWebhookRunFolder(selectedRunFolder)
  const [failedOnly, setFailedOnly] = useState(false)
  useEffect(() => { setFailedOnly(false) }, [selectedRunFolder])
  const sortedStepEntries = useMemo(
    () => Object.entries(logs?.steps || {}).sort(sortStepEntriesByExecution),
    [logs],
  )
  const failedEntries = useMemo(
    () => sortedStepEntries.filter(([, stepLogs]) => getStepStatus(stepLogs) === 'failed'),
    [sortedStepEntries],
  )
  // Open the first failed step once per run folder so the failure is the
  // first thing read. Never steals an explicit user selection, and never
  // re-fires on the 2.5 s silent polls.
  const autoExpandedFolders = useRef<Set<string>>(new Set())
  useEffect(() => {
    if (!logs || autoExpandedFolders.current.has(selectedRunFolder)) return
    autoExpandedFolders.current.add(selectedRunFolder)
    if (expandedSteps.size > 0 || failedEntries.length === 0) return
    toggleStep(failedEntries[0][0])
  }, [logs, selectedRunFolder, expandedSteps.size, failedEntries, toggleStep])
  const [webhookPayloadOpen, setWebhookPayloadOpen] = useState(false)
  const [webhookPayload, setWebhookPayload] = useState<string | null>(null)
  const [webhookPayloadLoading, setWebhookPayloadLoading] = useState(false)
  const [webhookPayloadError, setWebhookPayloadError] = useState<string | null>(null)

  useEffect(() => {
    setWebhookPayloadOpen(false)
    setWebhookPayload(null)
    setWebhookPayloadError(null)
  }, [selectedRunFolder])

  useEffect(() => {
    if (!webhookPayloadOpen || !webhookRun || !workspacePath || !selectedRunFolder || webhookPayload !== null || webhookPayloadLoading || webhookPayloadError !== null) return
    setWebhookPayloadLoading(true)
    setWebhookPayloadError(null)
    agentApi.getExecutionWebhookPayload(workspacePath, selectedRunFolder)
      .then(result => setWebhookPayload(formatLogFileContent(result.raw_payload)))
      .catch(err => {
        const responseBody = (err as { response?: { data?: unknown } })?.response?.data
        const detail = typeof responseBody === 'string' ? responseBody.trim() : err instanceof Error ? err.message : ''
        setWebhookPayloadError(detail || 'Webhook payload could not be loaded.')
      })
      .finally(() => setWebhookPayloadLoading(false))
  }, [webhookPayloadOpen, webhookRun, workspacePath, selectedRunFolder, webhookPayload, webhookPayloadLoading, webhookPayloadError])

  const webhookPayloadPanel = useMemo(() => {
    if (!webhookRun || !webhookPayloadOpen) return null
    return (
      <section className="overflow-hidden rounded-lg border border-border bg-card" aria-label="Webhook payload">
        <div className="flex items-center gap-2 border-b border-border px-3 py-2 text-xs font-medium text-foreground">
          <Braces className="h-3.5 w-3.5 text-primary" />
          Webhook payload
          <span className="font-normal text-muted-foreground">Raw request JSON for this run</span>
        </div>
        {webhookPayloadLoading ? (
          <div className="flex items-center gap-2 px-3 py-4 text-xs text-muted-foreground">
            <Loader2 className="h-3.5 w-3.5 animate-spin" /> Loading payload…
          </div>
        ) : webhookPayloadError ? (
          <div className="flex items-center justify-between gap-3 px-3 py-3 text-xs text-destructive">
            <span>{webhookPayloadError}</span>
            <button type="button" onClick={() => setWebhookPayloadError(null)} className="shrink-0 rounded border border-border px-2 py-1 text-foreground hover:bg-muted">Retry</button>
          </div>
        ) : (
          <pre className="max-h-80 overflow-auto whitespace-pre-wrap break-words bg-muted/25 p-3 font-mono text-[11px] leading-5 text-foreground">{webhookPayload || ''}</pre>
        )}
      </section>
    )
  }, [webhookRun, webhookPayloadOpen, webhookPayloadLoading, webhookPayloadError, webhookPayload])

  // Recursive render function for step content
  const renderStepContent = (stepId: string, stepLogs: StepExecutionLogs) => (
    <StepContent
      stepId={stepId}
      stepLogs={stepLogs}
      logs={logs}
      stepSearchQueries={stepSearchQueries}
      setStepSearchQueries={setStepSearchQueries}
      expandedValidations={expandedValidations}
      toggleValidation={toggleValidation}
      expandedExecutions={expandedExecutions}
      toggleExecution={toggleExecution}
      expandedArchived={expandedArchived}
      toggleArchived={toggleArchived}
      expandedFiles={expandedFiles}
      fileContents={fileContents}
      loadingFiles={loadingFiles}
      toggleFileExpansion={toggleFileExpansion}
    />
  )

  return (
    <InspectorShell
      className="bg-background flex flex-col border border-border relative h-full min-h-0 rounded-none border-0"
      headerClassName={headerRowClass}
      header={
        <LogsHeader
          startedAt={startedAt}
          runFolderOptions={runFolderOptions}
          runFolderInfos={runFolderInfos}
          selectedRunFolder={selectedRunFolder}
          setSelectedRunFolder={setSelectedRunFolder}
          loading={loading}
          loadLogs={loadLogs}
          onRefreshRunFolders={onRefreshRunFolders}
          showWebhookPayload={webhookRun}
          webhookPayloadOpen={webhookPayloadOpen}
          onToggleWebhookPayload={() => setWebhookPayloadOpen(open => !open)}
        />
      }
    >
        {/* Content */}
        <div
          className="flex-1 overflow-y-auto bg-background p-4"
          onScroll={focusedStepId ? handleStepDetailScroll : undefined}
        >
          {loading ? (
            <div className="flex flex-col items-center justify-center py-12 text-muted-foreground">
              <Loader2 className="w-8 h-8 animate-spin mb-3 text-primary" />
              <p>Loading execution logs...</p>
            </div>
          ) : error ? (
            <div className="flex flex-col items-center justify-center py-12 text-destructive">
              <AlertCircle className="w-12 h-12 mb-3" />
              <p>{error}</p>
              <button 
                onClick={() => loadLogs()}
                className="mt-4 px-4 py-2 bg-destructive/10 text-destructive rounded-md hover:bg-destructive/20 transition-colors text-sm font-medium"
              >
                Retry
              </button>
            </div>
          ) : !selectedRunFolder ? (
            <div className="flex flex-col items-center justify-center py-12 text-muted-foreground">
              <FileText className="w-12 h-12 mb-3 opacity-50" />
              <p className="text-sm font-medium">Select an iteration or group to view logs</p>
              <p className="text-xs mt-2 opacity-70">
                {runFolderOptions.length > 0
                  ? `Choose from ${runFolderOptions.length} available ${runFolderOptions.length === 1 ? 'run' : 'runs'} above.`
                  : 'No run folders available. Execute an automation to generate logs.'}
              </p>
            </div>
          ) : (
            <div className="space-y-4">
              {webhookPayloadPanel}

              {focusedStepId && (
                <WorkspacePanelBack label="Back to all steps" onBack={() => toggleStep(focusedStepId)} />
              )}

              {/* Message when no step logs found */}
              {logs && Object.keys(logs.steps).length === 0 && (
                <div className="flex flex-col items-center justify-center py-8 text-muted-foreground border border-dashed border-border rounded-lg">
                  <FileText className="w-10 h-10 mb-2 opacity-50" />
                  <p className="text-sm">No step execution logs found for <span className="font-mono text-xs bg-muted px-1.5 py-0.5 rounded">{selectedRunFolder}</span>.</p>
                  {runFolders.length > 1 && (
                    <p className="text-xs mt-2 opacity-70">
                      Try selecting a different iteration or group from the dropdown above.
                    </p>
                  )}
                </div>
              )}

              {!focusedStepId && sortedStepEntries.length > 0 && (
                <div className="flex flex-wrap items-center gap-1.5 pb-1">
                  <button
                    type="button"
                    onClick={() => setFailedOnly(value => !value)}
                    aria-pressed={failedOnly}
                    disabled={failedEntries.length === 0}
                    className={`inline-flex items-center gap-1 px-2.5 py-1 rounded-full text-xs font-medium border transition-colors disabled:cursor-default disabled:opacity-50 ${
                      failedOnly
                        ? 'bg-primary text-primary-foreground border-primary'
                        : 'bg-muted text-muted-foreground border-border hover:bg-accent'
                    }`}
                  >
                    Failed · {failedEntries.length}
                  </button>
                </div>
              )}

              {!focusedStepId && routingRouteGroups.length > 0 && (
                <div className="flex flex-wrap items-center gap-1.5 pb-1">
                  <button
                    type="button"
                    onClick={() => setRouteFilterKey(null)}
                    className={`inline-flex items-center gap-1 px-2.5 py-1 rounded-full text-xs font-medium border transition-colors ${
                      routeFilterKey === null
                        ? 'bg-primary text-primary-foreground border-primary'
                        : 'bg-muted text-muted-foreground border-border hover:bg-accent'
                    }`}
                  >
                    All steps
                  </button>
                  {routingRouteGroups.map(group => (
                    <button
                      key={group.key}
                      type="button"
                      onClick={() => setRouteFilterKey(group.key)}
                      title={`Route "${group.routeName}" -- selected by ${group.routeStepTitle}`}
                      className={`inline-flex items-center gap-1 px-2.5 py-1 rounded-full text-xs font-medium border transition-colors ${
                        routeFilterKey === group.key
                          ? 'bg-primary text-primary-foreground border-primary'
                          : 'bg-muted text-muted-foreground border-border hover:bg-accent'
                      }`}
                    >
                      <RouteIcon className="h-3 w-3" />
                      {group.routeName}
                    </button>
                  ))}
                </div>
              )}

              <StepList
                logs={logs}
                focusedStepId={focusedStepId}
                routeFilterKey={routeFilterKey}
                showFailedOnly={failedOnly}
                expandedSteps={expandedSteps}
                toggleStep={toggleStep}
                renderStepContent={renderStepContent}
              />
            </div>
          )}
        </div>
    </InspectorShell>
  )
}

export default ExecutionLogsPopup
