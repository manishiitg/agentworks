import React, { useCallback, useEffect, useState } from 'react'
import { AlertCircle, Check, ChevronRight, Copy, ExternalLink, Eye, EyeOff, Globe, Loader2, LockKeyhole, X } from 'lucide-react'
import type { WorkflowPublishInfoResponse, WorkflowPublishStrategyInfo } from '../../services/api-types'
import { formatPublishStateLabel, getPublishStateVisual } from '../workflow/publishStatus'
import { WorkspaceViewHeader } from '../workflow/WorkspaceViewHeader'
import { WorkspaceViewIconButton } from '../workflow/WorkspaceViewIconButton'
import {
  extractErrorMessage,
  findPublishDestinationStatus,
  formatRelativeTime,
  publishDestinationTitle,
} from './popupUtils'

export interface PublishPopupProps {
  loadInfo: () => Promise<WorkflowPublishInfoResponse>
  onStateLoaded?: (state: string) => void
  fallbackStrategies: WorkflowPublishStrategyInfo[]
  subtitle: string
  emptyDestinationsText: string
  destinationsHelp: string
  supportedTitle?: string
  supportedHelp?: string
  defaultTargetLabel: string
  getSummary: (info: WorkflowPublishInfoResponse | null) => string
  loadAccessSecret?: (secretName: string) => Promise<string>
  loadErrorMessage?: string
  showEnabledBadge?: boolean
  headerAction?: React.ReactNode
}

const formatTargets = (info: WorkflowPublishInfoResponse | null, fallback: string): string => {
  const targets = info?.config?.targets?.length ? info.config.targets : (info?.status?.targets || [])
  return targets.length
    ? targets.map(target => (typeof target === 'string' ? target : (target.id || target.artifact || 'artifact'))).join(', ')
    : fallback
}

type PublishAccessInfo = {
  mode: 'public' | 'private' | 'unguessable'
  label: string
  detail: string
  secretName?: string
}

const normalizeText = (value?: string): string => (value || '').trim()

const extractSecretNameFromText = (text: string): string | undefined => {
  const match = text.match(/(?:workflow\s+secret|secret)\s+([A-Z][A-Z0-9_]{2,})\b/i)
  return match?.[1]
}

const secretLooksLikePassword = (secretName?: string): boolean => (
  Boolean(secretName && /(PASSWORD|PASS|PASSPHRASE|STATICRYPT)/i.test(secretName))
)

const readDestinationAccessInfo = (
  visibility?: string,
  secretName?: string,
  fallback?: PublishAccessInfo
): PublishAccessInfo => {
  const normalizedVisibility = normalizeText(visibility).toLowerCase()
  const usableSecretName = secretLooksLikePassword(secretName) ? secretName : undefined

  if (
    normalizedVisibility === 'private' ||
    normalizedVisibility === 'password' ||
    normalizedVisibility === 'password-protected' ||
    normalizedVisibility === 'protected' ||
    usableSecretName
  ) {
    return {
      mode: 'private',
      label: 'Password protected',
      detail: usableSecretName ? `Secret: ${usableSecretName}` : 'Password required',
      secretName: usableSecretName,
    }
  }

  if (normalizedVisibility === 'unguessable-link') {
    return {
      mode: 'unguessable',
      label: 'Unguessable link',
      detail: 'Anyone with the URL can open it',
    }
  }

  return fallback || {
    mode: 'public',
    label: 'Public',
    detail: 'Anyone with the URL can open it',
  }
}

const getPublishAccessInfo = (info: WorkflowPublishInfoResponse | null): PublishAccessInfo => {
  const statusVisibility = normalizeText(info?.status?.visibility)
  const destinationVisibility = info?.config?.destinations
    ?.map(destination => normalizeText(destination.visibility))
    .find(Boolean)
  const summaryText = [info?.status?.summary, info?.config?.notes, ...(info?.config?.destinations || []).map(destination => destination.notes)]
    .filter(Boolean)
    .join(' ')
  const summaryImpliesPassword = /staticrypt|password[-\s]?protected|password gate|passphrase/i.test(summaryText)
  const statusSecretName = normalizeText(info?.status?.secret_name)
  const inferredSecretName = statusSecretName || extractSecretNameFromText(summaryText)

  if (statusVisibility || summaryImpliesPassword || inferredSecretName) {
    return readDestinationAccessInfo(
      statusVisibility || (summaryImpliesPassword ? 'private' : undefined),
      inferredSecretName,
    )
  }

  return readDestinationAccessInfo(destinationVisibility)
}

const PublishPopupBody: React.FC<PublishPopupProps> = ({
  loadInfo,
  onStateLoaded,
  fallbackStrategies,
  subtitle,
  emptyDestinationsText,
  destinationsHelp,
  supportedTitle = 'Common hosts',
  supportedHelp,
  defaultTargetLabel,
  getSummary,
  loadAccessSecret,
  loadErrorMessage = 'Failed to load publish status',
  showEnabledBadge = false,
  headerAction,
}) => {
  const [loading, setLoading] = useState(false)
  const [info, setInfo] = useState<WorkflowPublishInfoResponse | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [copied, setCopied] = useState(false)
  const [accessSecretValue, setAccessSecretValue] = useState<string | null>(null)
  const [accessSecretVisible, setAccessSecretVisible] = useState(false)
  const [accessSecretLoading, setAccessSecretLoading] = useState(false)
  const [accessSecretCopied, setAccessSecretCopied] = useState(false)
  const [accessSecretError, setAccessSecretError] = useState<string | null>(null)
  const accessInfo = getPublishAccessInfo(info)

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

  useEffect(() => {
    setAccessSecretValue(null)
    setAccessSecretVisible(false)
    setAccessSecretCopied(false)
    setAccessSecretError(null)
  }, [accessInfo.secretName])

  const copyUrl = async (url: string) => {
    try {
      await navigator.clipboard.writeText(url)
      setCopied(true)
      setTimeout(() => setCopied(false), 2000)
    } catch { /* clipboard unavailable */ }
  }

  const fetchAccessSecret = async (): Promise<string | null> => {
    if (!accessInfo.secretName || !loadAccessSecret) return null
    if (accessSecretValue !== null) return accessSecretValue
    setAccessSecretLoading(true)
    setAccessSecretError(null)
    try {
      const value = await loadAccessSecret(accessInfo.secretName)
      setAccessSecretValue(value)
      return value
    } catch (err) {
      setAccessSecretError(extractErrorMessage(err, 'Failed to load publish password'))
      return null
    } finally {
      setAccessSecretLoading(false)
    }
  }

  const toggleAccessSecretVisible = async () => {
    if (accessSecretVisible) {
      setAccessSecretVisible(false)
      return
    }
    const value = await fetchAccessSecret()
    if (value !== null) setAccessSecretVisible(true)
  }

  const copyAccessSecret = async () => {
    const value = await fetchAccessSecret()
    if (!value) return
    try {
      await navigator.clipboard.writeText(value)
      setAccessSecretCopied(true)
      setTimeout(() => setAccessSecretCopied(false), 2000)
    } catch { /* clipboard unavailable */ }
  }

  const state = info?.effective_state || 'not_configured'
  const visual = getPublishStateVisual(state)
  const StateIcon = visual.Icon
  const configEnabled = Boolean(info?.config?.enabled)
  const destinations = info?.config?.destinations || []
  const supported = info?.supported?.length ? info.supported : fallbackStrategies
  const url = info?.url || info?.status?.url || ''

  return (
        <div className="flex h-full min-h-0 w-full max-w-none flex-col bg-background">
          <WorkspaceViewHeader
            icon={Globe}
            title="Publish"
            subtitle={subtitle}
            actions={<>
              {headerAction}
              <WorkspaceViewIconButton label="Refresh publish status" onClick={() => { void load() }} disabled={loading} spinning={loading} />
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
                          {formatPublishStateLabel(state)}
                        </span>
                      </div>
                      <p className="mt-2 max-w-2xl text-sm text-muted-foreground">{getSummary(info)}</p>
                      {info?.status?.last_error && <p className="mt-2 text-xs text-destructive">{info.status.last_error}</p>}
                    </div>
                  </div>

                  {url && (
                    <div className="flex items-center gap-2 border-t border-border px-4 py-3">
                      <Globe className="h-4 w-4 flex-shrink-0 text-muted-foreground" />
                      <a href={url} target="_blank" rel="noopener noreferrer" className="min-w-0 flex-1 truncate text-sm font-medium text-primary hover:underline" title={url}>{url}</a>
                      {accessInfo.mode === 'private' && (
                        <span className="hidden items-center gap-1 rounded-full border border-amber-500/30 bg-amber-500/10 px-2 py-0.5 text-[11px] font-medium text-amber-700 dark:text-amber-300 sm:inline-flex">
                          <LockKeyhole className="h-3 w-3" />
                          Password
                        </span>
                      )}
                      <button onClick={() => { void copyUrl(url) }} className="inline-flex h-7 w-7 items-center justify-center rounded-md border border-border text-muted-foreground hover:bg-muted hover:text-foreground" aria-label="Copy URL">
                        {copied ? <Check className="h-3.5 w-3.5 text-emerald-500" /> : <Copy className="h-3.5 w-3.5" />}
                      </button>
                      <a href={url} target="_blank" rel="noopener noreferrer" className="inline-flex h-7 w-7 items-center justify-center rounded-md border border-border text-muted-foreground hover:bg-muted hover:text-foreground" aria-label="Open URL">
                        <ExternalLink className="h-3.5 w-3.5" />
                      </a>
                    </div>
                  )}

                  <div className="grid border-t border-border text-sm sm:grid-cols-4">
                    <div className="border-b border-border px-4 py-3 sm:border-b-0 sm:border-r">
                      <div className="text-xs text-muted-foreground">Last published</div>
                      <div className="mt-1 font-medium text-foreground">{formatRelativeTime(info?.status?.last_published_at)}</div>
                    </div>
                    <div className="border-b border-border px-4 py-3 sm:border-b-0 sm:border-r">
                      <div className="text-xs text-muted-foreground">Last attempt</div>
                      <div className="mt-1 font-medium text-foreground">{formatRelativeTime(info?.status?.last_attempt_at)}</div>
                    </div>
                    <div className="px-4 py-3">
                      <div className="text-xs text-muted-foreground">Publishing</div>
                      <div className="mt-1 font-medium text-foreground">{formatTargets(info, defaultTargetLabel)}</div>
                    </div>
                    <div className="border-t border-border px-4 py-3 sm:border-t-0 sm:border-l">
                      <div className="text-xs text-muted-foreground">Access</div>
                      <div className="mt-1 flex items-center gap-1.5 font-medium text-foreground">
                        {accessInfo.mode === 'private' && <LockKeyhole className="h-3.5 w-3.5 text-amber-500" />}
                        {accessInfo.label}
                      </div>
                      <div className="mt-0.5 truncate text-xs text-muted-foreground" title={accessInfo.detail}>
                        {accessInfo.detail}
                      </div>
                      {accessInfo.mode === 'private' && accessInfo.secretName && loadAccessSecret && (
                        <div className="mt-2 space-y-1.5">
                          {accessSecretVisible && accessSecretValue && (
                            <div className="rounded border border-amber-500/20 bg-amber-500/10 px-2 py-1 text-xs font-medium text-amber-800 dark:text-amber-200 break-all">
                              {accessSecretValue}
                            </div>
                          )}
                          <div className="flex flex-wrap items-center gap-1.5">
                            <button
                              type="button"
                              onClick={() => { void toggleAccessSecretVisible() }}
                              disabled={accessSecretLoading}
                              className="inline-flex h-7 items-center gap-1 rounded-md border border-border px-2 text-[11px] text-muted-foreground transition-colors hover:bg-muted hover:text-foreground disabled:opacity-50"
                              aria-label={accessSecretVisible ? 'Hide publish password' : 'Reveal publish password'}
                            >
                              {accessSecretLoading ? <Loader2 className="h-3 w-3 animate-spin" /> : accessSecretVisible ? <EyeOff className="h-3 w-3" /> : <Eye className="h-3 w-3" />}
                              {accessSecretVisible ? 'Hide' : 'Reveal'}
                            </button>
                            <button
                              type="button"
                              onClick={() => { void copyAccessSecret() }}
                              disabled={accessSecretLoading}
                              className="inline-flex h-7 items-center gap-1 rounded-md border border-border px-2 text-[11px] text-muted-foreground transition-colors hover:bg-muted hover:text-foreground disabled:opacity-50"
                              aria-label="Copy publish password"
                            >
                              {accessSecretCopied ? <Check className="h-3 w-3 text-emerald-500" /> : <Copy className="h-3 w-3" />}
                              {accessSecretCopied ? 'Copied' : 'Copy password'}
                            </button>
                          </div>
                          {accessSecretError && (
                            <div className="text-[11px] text-destructive">{accessSecretError}</div>
                          )}
                        </div>
                      )}
                    </div>
                  </div>
                </section>

                <section className="rounded-md border border-border">
                  <div className="flex items-center justify-between gap-3 border-b border-border px-4 py-3">
                    <div>
                      <h3 className="text-sm font-semibold text-foreground">Destinations</h3>
                      <p className="mt-0.5 text-xs text-muted-foreground">{destinationsHelp}</p>
                    </div>
                    <span className="rounded-full bg-muted px-2 py-0.5 text-xs text-muted-foreground">{destinations.length}</span>
                  </div>
                  {destinations.length === 0 ? (
                    <div className="px-4 py-5 text-sm text-muted-foreground">{emptyDestinationsText}</div>
                  ) : (
                    <div className="divide-y divide-border">
                      {destinations.map(destination => {
                        const status = findPublishDestinationStatus(info?.status?.destinations, destination)
                        const destinationState = status?.state || 'configured_not_verified'
                        const destinationVisual = getPublishStateVisual(destinationState)
                        const DestinationIcon = destinationVisual.Icon
                        const destinationAccess = readDestinationAccessInfo(
                          destination.visibility,
                          destination.secret_name,
                          destinations.length === 1 ? accessInfo : undefined,
                        )
                        return (
                          <div key={destination.id || publishDestinationTitle(destination)} className="flex flex-col gap-2 px-4 py-3 sm:flex-row sm:items-start sm:justify-between">
                            <div className="min-w-0">
                              <div className="flex flex-wrap items-center gap-2">
                                <DestinationIcon className={`h-3.5 w-3.5 ${destinationVisual.icon}`} />
                                <span className="text-sm font-medium text-foreground">{destination.id || 'Destination'}</span>
                                <span className="rounded bg-muted px-1.5 py-0.5 text-[11px] text-muted-foreground">{destination.provider}</span>
                                {destination.method && <span className="rounded bg-muted px-1.5 py-0.5 text-[11px] text-muted-foreground">{destination.method}</span>}
                                {destinationAccess.mode !== 'public' && (
                                  <span className="inline-flex items-center gap-1 rounded bg-amber-500/10 px-1.5 py-0.5 text-[11px] text-amber-700 dark:text-amber-300">
                                    {destinationAccess.mode === 'private' && <LockKeyhole className="h-3 w-3" />}
                                    {destinationAccess.label}
                                  </span>
                                )}
                              </div>
                              <div className="mt-1 truncate text-xs text-muted-foreground">{publishDestinationTitle(destination)}</div>
                              {status?.url && <a href={status.url} target="_blank" rel="noopener noreferrer" className="mt-1 inline-block truncate text-xs text-primary hover:underline">{status.url}</a>}
                              {status?.error && <div className="mt-1 text-xs text-destructive">{status.error}</div>}
                            </div>
                            <div className="flex flex-wrap items-center gap-2 text-xs text-muted-foreground sm:justify-end">
                              <span className={`rounded-full border px-2 py-0.5 ${destinationVisual.badge}`}>{formatPublishStateLabel(destinationState)}</span>
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
                    {supportedTitle}
                    <span className="rounded-full bg-muted px-2 py-0.5 text-xs font-medium text-muted-foreground">{supported.length}</span>
                  </summary>
                  <div className="border-t border-border px-4 py-3">
                    {supportedHelp && <p className="mb-3 text-xs text-muted-foreground">{supportedHelp}</p>}
                    <div className="grid divide-y divide-border md:grid-cols-2 md:divide-x md:divide-y-0">
                      {supported.map(strategy => (
                        <div key={strategy.id} className="flex items-start justify-between gap-3 px-4 py-3 first:pl-0">
                          <div className="min-w-0">
                            <div className="flex items-center gap-2 text-sm font-medium text-foreground">
                              <Globe className="h-3.5 w-3.5 shrink-0 text-muted-foreground" />
                              {strategy.label}
                              <span className="rounded bg-muted px-1.5 py-0.5 text-[11px] text-muted-foreground">{strategy.method}</span>
                            </div>
                            <p className="mt-1 text-xs leading-5 text-muted-foreground">{strategy.description}</p>
                          </div>
                        </div>
                      ))}
                    </div>
                  </div>
                </details>
              </div>
            )}
          </div>
        </div>
  )
}

export default PublishPopupBody
