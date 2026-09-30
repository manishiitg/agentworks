import React, { useCallback, useEffect, useState } from 'react'
import { AlertCircle, ChevronRight, Cloud, GitBranch, Loader2, X } from 'lucide-react'
import type { WorkflowBackupInfoResponse, WorkflowBackupStrategyInfo } from '../../services/api-types'
import { formatBackupStateLabel, getBackupStateVisual } from '../workflow/backupStatus'
import { AskAIButton } from '../workflow/AskAIButton'
import { WorkspaceViewHeader } from '../workflow/WorkspaceViewHeader'
import { WorkspaceViewIconButton } from '../workflow/WorkspaceViewIconButton'
import {
  backupDestinationTitle,
  coverageText,
  extractErrorMessage,
  findBackupDestinationStatus,
  formatRelativeTime,
  type StrategyAskContext,
} from './popupUtils'

export interface BackupPopupProps {
  loadInfo: () => Promise<WorkflowBackupInfoResponse>
  onStateLoaded?: (state: string) => void
  fallbackStrategies: WorkflowBackupStrategyInfo[]
  subtitle: string
  emptyDestinationsText: string
  destinationsHelp: string
  getSummary: (info: WorkflowBackupInfoResponse | null) => string
  askContext?: StrategyAskContext
  loadErrorMessage?: string
  showEnabledBadge?: boolean
  headerAction?: React.ReactNode
}

const BackupPopupBody: React.FC<BackupPopupProps> = ({
  loadInfo,
  onStateLoaded,
  fallbackStrategies,
  subtitle,
  emptyDestinationsText,
  destinationsHelp,
  getSummary,
  askContext,
  loadErrorMessage = 'Failed to load backup status',
  showEnabledBadge = false,
  headerAction,
}) => {
  const [loading, setLoading] = useState(false)
  const [info, setInfo] = useState<WorkflowBackupInfoResponse | null>(null)
  const [error, setError] = useState<string | null>(null)

  const load = useCallback(async () => {
    setLoading(true)
    setError(null)
    try {
      const resp = await loadInfo()
      setInfo(resp)
      onStateLoaded?.(resp.effective_state || 'not_configured')
    } catch (err) {
      setError(extractErrorMessage(err, loadErrorMessage))
    } finally {
      setLoading(false)
    }
  }, [loadErrorMessage, loadInfo, onStateLoaded])

  useEffect(() => {
    void load()
  }, [load])

  const state = info?.effective_state || 'not_configured'
  const visual = getBackupStateVisual(state)
  const StateIcon = visual.Icon
  const configEnabled = Boolean(info?.config?.enabled)
  const destinations = info?.config?.destinations || []
  const supportedStrategies = info?.supported?.length ? info.supported : fallbackStrategies

  return (
        <div className="flex h-full min-h-0 w-full max-w-none flex-col bg-background">
          <WorkspaceViewHeader
            icon={Cloud}
            title="Backup"
            subtitle={subtitle}
            actions={<>
              {headerAction}
              <WorkspaceViewIconButton label="Refresh backup status" onClick={() => { void load() }} disabled={loading} spinning={loading} />
            </>}
          />

          {error && (
            <div className="flex items-center gap-2 bg-destructive/10 px-5 py-2 text-xs text-destructive">
              <AlertCircle className="h-3.5 w-3.5 flex-shrink-0" />
              <span className="min-w-0 flex-1">{error}</span>
              <button onClick={() => setError(null)} className="text-destructive/70 hover:text-destructive" aria-label="Dismiss error">
                <X className="h-3 w-3" />
              </button>
            </div>
          )}

          <div className="flex-1 overflow-y-auto px-4 py-4 sm:px-5">
            {loading && !info ? (
              <div className="flex items-center justify-center py-12">
                <Loader2 className="h-5 w-5 animate-spin text-muted-foreground" />
              </div>
            ) : (
              <div className="space-y-4">
                <section className="overflow-hidden rounded-md border border-border">
                  <div className="flex flex-col gap-3 bg-muted/30 px-4 py-4 sm:flex-row sm:items-start sm:justify-between">
                    <div className="min-w-0">
                      <div className="flex flex-wrap items-center gap-2">
                        <StateIcon className={`h-4 w-4 ${visual.icon}`} />
                        {showEnabledBadge && (
                          <span className={`inline-flex items-center rounded-full border px-2 py-0.5 text-xs font-medium ${
                            configEnabled
                              ? 'border-emerald-500/30 bg-emerald-500/10 text-emerald-700 dark:text-emerald-300'
                              : 'border-border bg-background text-muted-foreground'
                          }`}>
                            {configEnabled ? 'Enabled' : 'Disabled'}
                          </span>
                        )}
                        <span className={`inline-flex items-center rounded-full border px-2 py-0.5 text-xs font-medium ${visual.badge}`}>
                          {formatBackupStateLabel(state)}
                        </span>
                      </div>
                      <p className="mt-2 max-w-2xl text-sm text-muted-foreground">{getSummary(info)}</p>
                      {info?.status?.last_error && <p className="mt-2 text-xs text-destructive">{info.status.last_error}</p>}
                    </div>
                    <div className="flex flex-wrap items-center gap-2">
                      {askContext && (
                        <AskAIButton workspacePath={askContext.workspacePath} label="Set up" message="/backup" />
                      )}
                    </div>
                  </div>

                  <div className="grid border-t border-border text-sm sm:grid-cols-3">
                    <div className="border-b border-border px-4 py-3 sm:border-b-0 sm:border-r">
                      <div className="text-xs text-muted-foreground">Last success</div>
                      <div className="mt-1 font-medium text-foreground">{formatRelativeTime(info?.status?.last_success_at)}</div>
                    </div>
                    <div className="border-b border-border px-4 py-3 sm:border-b-0 sm:border-r">
                      <div className="text-xs text-muted-foreground">Last attempt</div>
                      <div className="mt-1 font-medium text-foreground">{formatRelativeTime(info?.status?.last_attempt_at)}</div>
                    </div>
                    <div className="px-4 py-3">
                      <div className="text-xs text-muted-foreground">Tracked files</div>
                      <div className="mt-1 font-medium text-foreground">{info?.tracked_files_count ?? 0}</div>
                    </div>
                  </div>
                </section>

                <section className="rounded-md border border-border">
                  <div className="flex items-center justify-between gap-3 border-b border-border px-4 py-3">
                    <div>
                      <h3 className="text-sm font-semibold text-foreground">Configured destinations</h3>
                      <p className="mt-0.5 text-xs text-muted-foreground">{destinationsHelp}</p>
                    </div>
                    <span className="rounded-full bg-muted px-2 py-0.5 text-xs text-muted-foreground">{destinations.length}</span>
                  </div>
                  {destinations.length === 0 ? (
                    <div className="px-4 py-5 text-sm text-muted-foreground">{emptyDestinationsText}</div>
                  ) : (
                    <div className="divide-y divide-border">
                      {destinations.map(destination => {
                        const status = findBackupDestinationStatus(info?.status?.destinations, destination)
                        const destinationState = status?.state || 'configured_not_verified'
                        const destinationVisual = getBackupStateVisual(destinationState)
                        const DestinationIcon = destinationVisual.Icon
                        return (
                          <div key={destination.id || backupDestinationTitle(destination)} className="flex flex-col gap-2 px-4 py-3 sm:flex-row sm:items-start sm:justify-between">
                            <div className="min-w-0">
                              <div className="flex flex-wrap items-center gap-2">
                                <DestinationIcon className={`h-3.5 w-3.5 ${destinationVisual.icon}`} />
                                <span className="text-sm font-medium text-foreground">{destination.id || 'Destination'}</span>
                                <span className="rounded bg-muted px-1.5 py-0.5 text-[11px] text-muted-foreground">{destination.provider || destination.type}</span>
                              </div>
                              <div className="mt-1 truncate text-xs text-muted-foreground">{backupDestinationTitle(destination)}</div>
                              <div className="mt-1 text-xs text-muted-foreground">Covers: {coverageText(destination.covers)}</div>
                              {status?.error && <div className="mt-1 text-xs text-destructive">{status.error}</div>}
                            </div>
                            <div className="flex flex-wrap items-center gap-2 text-xs text-muted-foreground sm:justify-end">
                              <span className={`rounded-full border px-2 py-0.5 ${destinationVisual.badge}`}>{formatBackupStateLabel(destinationState)}</span>
                              {typeof status?.objects_synced === 'number' && status.objects_synced > 0 && <span>{status.objects_synced} objects</span>}
                              {status?.last_success_at && <span>{formatRelativeTime(status.last_success_at)}</span>}
                            </div>
                          </div>
                        )
                      })}
                    </div>
                  )}
                </section>

                <details className="group rounded-md border border-border">
                  <summary className="flex cursor-pointer list-none items-center gap-2 px-4 py-3 text-sm font-semibold text-foreground">
                    <ChevronRight className="h-4 w-4 shrink-0 text-muted-foreground transition-transform group-open:rotate-90" />
                    Supported strategies
                    <span className="rounded-full bg-muted px-2 py-0.5 text-xs font-medium text-muted-foreground">{supportedStrategies.length}</span>
                  </summary>
                  <div className="grid divide-y divide-border border-t border-border md:grid-cols-2 md:divide-x md:divide-y-0">
                    {supportedStrategies.map((strategy) => (
                      <div key={strategy.id} className="flex items-start justify-between gap-3 px-4 py-3">
                        <div className="min-w-0">
                          <div className="flex items-center gap-2 text-sm font-medium text-foreground">
                            {strategy.id === 'git' ? <GitBranch className="h-3.5 w-3.5 shrink-0 text-muted-foreground" /> : <Cloud className="h-3.5 w-3.5 shrink-0 text-muted-foreground" />}
                            {strategy.label}
                          </div>
                          <p className="mt-1 text-xs leading-5 text-muted-foreground">{strategy.description}</p>
                          {strategy.best_for && strategy.best_for.length > 0 && (
                            <div className="mt-1.5 flex flex-wrap gap-1">
                              {strategy.best_for.slice(0, 4).map(tag => (
                                <span key={tag} className="rounded bg-muted px-1.5 py-0.5 text-[11px] text-muted-foreground">{tag}</span>
                              ))}
                            </div>
                          )}
                        </div>
                        {askContext && (
                          <AskAIButton
                            workspacePath={askContext.workspacePath}
                            iconOnly
                            message={`Help me ${askContext.strategyVerb} ${strategy.label}. Explain what I need and walk me through it.`}
                          />
                        )}
                      </div>
                    ))}
                  </div>
                </details>
              </div>
            )}
          </div>
        </div>
  )
}

export default BackupPopupBody
