import { useCallback, useEffect, useState } from 'react'
import {
  AlertCircle,
  Ban,
  BellRing,
  CheckCircle2,
  Loader2,
  Mail,
} from 'lucide-react'
import {
  loadWorkflowNotificationInfo,
  type WorkflowNotificationInfo,
  type WorkflowNotificationState,
} from '../../services/workflow-notifications'
import { workflowManifestApi } from '../../services/api'
import { READ_ONLY_TITLE, useCanWriteWorkflow } from '../../hooks/useCanWriteWorkflow'
import { formatNotificationStateLabel } from './notificationStatus'
import NotificationInstructions from './NotificationInstructions'
import { WorkspaceViewHeader } from './WorkspaceViewHeader'
import { WorkspaceViewIconButton } from './WorkspaceViewIconButton'

interface WorkflowNotificationViewProps {
  workspacePath: string | null
  onStateLoaded?: (state: WorkflowNotificationState) => void
  loadInfo?: () => Promise<WorkflowNotificationInfo>
}

const stateBadgeClass = (state: WorkflowNotificationState): string => {
  switch (state) {
    case 'ready':
      return 'border-emerald-500/30 bg-emerald-500/10 text-emerald-700 dark:text-emerald-300'
    case 'missing_secret':
    case 'invalid_secret':
      return 'border-amber-500/30 bg-amber-500/10 text-amber-700 dark:text-amber-300'
    default:
      return 'border-border bg-background text-muted-foreground'
  }
}

export default function WorkflowNotificationView({
  workspacePath,
  onStateLoaded,
  loadInfo,
}: WorkflowNotificationViewProps) {
  const [loading, setLoading] = useState(false)
  const [info, setInfo] = useState<WorkflowNotificationInfo | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [senderSelection, setSenderSelection] = useState('')
  const [breakSaving, setBreakSaving] = useState(false)
  const [senderSaving, setSenderSaving] = useState(false)
  const [senderError, setSenderError] = useState<string | null>(null)
  const canWriteWorkflow = useCanWriteWorkflow(workspacePath)

  const load = useCallback(async () => {
    if (!workspacePath && !loadInfo) return
    setLoading(true)
    setError(null)
    try {
      const next = loadInfo ? await loadInfo() : await loadWorkflowNotificationInfo(workspacePath as string)
      setInfo(next)
      setSenderSelection(next.gmailConnectionId || '')
      onStateLoaded?.(next.effectiveState)
    } catch (loadError) {
      setError(loadError instanceof Error ? loadError.message : 'Failed to load notification status')
    } finally {
      setLoading(false)
    }
  }, [loadInfo, onStateLoaded, workspacePath])

  const saveSender = async (connectionId: string) => {
    if (!workspacePath || !canWriteWorkflow || senderSaving) return
    const previous = senderSelection
    setSenderSelection(connectionId)
    setSenderSaving(true)
    setSenderError(null)
    try {
      await workflowManifestApi.updateWorkflowManifest({
        workspace_path: workspacePath,
        notification_gmail_connection_id: connectionId,
      })
      await load()
    } catch (saveError) {
      setSenderSelection(previous)
      setSenderError(saveError instanceof Error ? saveError.message : 'Failed to save Notify sender')
    } finally {
      setSenderSaving(false)
    }
  }

  const saveBreakAlerts = async (enabled: boolean) => {
    if (!workspacePath || !canWriteWorkflow || breakSaving) return
    setBreakSaving(true)
    setError(null)
    try {
      await workflowManifestApi.updateWorkflowManifest({ workspace_path: workspacePath, break_alerts: enabled })
      await load()
    } catch (saveError) {
      setError(saveError instanceof Error ? saveError.message : 'Failed to save alerts')
    } finally { setBreakSaving(false) }
  }

  useEffect(() => {
    void load()
  }, [load])

  // Gmail auth resolves in a background subprocess on the server (~5s), so the
  // first read returns state="checking" rather than making the popup wait. Poll
  // until it settles; this is the only field that arrives late.
  useEffect(() => {
    if (info?.gmail?.state !== 'checking') return
    const timer = setTimeout(() => { void load() }, 1500)
    return () => clearTimeout(timer)
  }, [info?.gmail?.state, load])

  const state = info?.effectiveState || 'not_configured'
  const StateIcon = state === 'ready' ? CheckCircle2 : state === 'missing_secret' || state === 'invalid_secret' ? AlertCircle : BellRing
  const gmailReady = info?.gmail?.state === 'ready'
  const gmailChecking = info?.gmail?.state === 'checking'
  const gmailBlocked = info?.gmail?.blocked_recipients || []
  const gmailSender = info?.gmail?.default_sender?.trim() || ''
  const gmailSenderChoices = info?.gmail?.sender_choices || []
  const scopeName = info?.scopeLabel || workspacePath?.split('/').filter(Boolean).pop() || 'Workflow'
  const senderLabel = (id: string) => {
    const choice = gmailSenderChoices.find(entry => entry.id === id)
    return choice ? (choice.email || choice.display_name || id) : id
  }
  // Run outcomes stay visible in Activity. Team delivery uses saved destinations.
  // Summary content and recipients remain edited through /notify in chat.
  // The single Notify sender is selected directly above these summaries.
  const summaries = info ? [
    {
      key: 'run',
      title: 'Run summary',
      instructions: info.runSummaryInstructions.trim(),
      channels: info.runSummaryChannels,
      senders: info.gmailConnectionId ? [senderLabel(info.gmailConnectionId)] : [],
      recipients: info.runSummaryRecipients,
    },
  ] : []
  const overrides = info ? [
    ...info.excludeChannels.map(channel => ({ key: `x-${channel}`, text: `No ${channel}` })),
    ...gmailBlocked.map(email => ({ key: `b-${email}`, text: `Never to ${email}` })),
  ] : []
  // Colour carries meaning, sparingly: channels sky, sender violet, recipients
  // green, warnings amber, everything else neutral -- so a row can be read by
  // shape before its labels (user: "white on light white", 2026-09-03).
  const chipBase = 'inline-flex max-w-full items-center truncate rounded-full border px-2 py-0.5 text-xs'
  const chipMuted = `${chipBase} border-dashed border-border bg-background text-muted-foreground`
  const chipWarn = `${chipBase} border-amber-500/30 bg-amber-500/10 text-amber-700 dark:text-amber-300`
  const chipChannel = `${chipBase} border-sky-500/30 bg-sky-500/10 text-sky-700 dark:text-sky-300`
  const chipFrom = `${chipBase} border-violet-500/30 bg-violet-500/10 text-violet-700 dark:text-violet-300`
  const chipTo = `${chipBase} border-emerald-500/30 bg-emerald-500/10 text-emerald-700 dark:text-emerald-300`
  const rowClass = 'flex flex-wrap items-center gap-x-3 gap-y-1.5 px-4 py-2.5'
  const labelClass = 'flex w-28 shrink-0 items-center gap-1.5 text-xs font-medium text-foreground'
  const sectionHeadClass = 'flex items-center gap-2 border-b border-border bg-muted/50 px-4 py-2 text-sm font-semibold text-foreground'
  const stateLine = state === 'ready'
    ? 'Notifications on'
    : state === 'missing_secret'
      ? 'Slack secret missing'
      : state === 'invalid_secret'
        ? 'Slack URL invalid'
        : 'No channel set up yet'

  return (
        <div className="flex h-full min-h-0 w-full max-w-none flex-col bg-background">
          <WorkspaceViewHeader
            icon={BellRing}
            title="Notify"
            context={<span className={`inline-flex items-center rounded-full border px-2 py-0.5 text-xs font-medium ${stateBadgeClass(state)}`}>
              <StateIcon className="mr-1 h-3 w-3" />{formatNotificationStateLabel(state)}
            </span>}
            subtitle={`${scopeName} · ${stateLine}`}
            actions={<WorkspaceViewIconButton label="Refresh notification status" onClick={() => { void load() }} spinning={loading} />}
          />

          {error && (
            <div className="flex items-center gap-2 bg-destructive/10 px-5 py-2 text-xs text-destructive">
              <AlertCircle className="h-3.5 w-3.5 flex-shrink-0" />
              <span className="min-w-0 flex-1">{error}</span>
            </div>
          )}

          <div className="flex-1 overflow-y-auto px-4 py-4 sm:px-5">
            {loading && !info ? (
              <div className="flex items-center justify-center py-12"><Loader2 className="h-5 w-5 animate-spin text-muted-foreground" /></div>
            ) : info ? (
              <div className="space-y-4">
                <section className="rounded-md border border-border p-4">
                  <label className="flex items-center gap-3 text-sm font-medium">
                    <input type="checkbox" checked={info.breakAlerts} disabled={!workspacePath || !canWriteWorkflow || breakSaving}
                      onChange={event => { void saveBreakAlerts(event.target.checked) }} />
                    Tell me when something breaks
                  </label>
                  <p className="mt-2 text-xs text-muted-foreground">Show run failures in Needs you. Questions that need your answer and configured team messages stay available.</p>
                </section>
                <section className="rounded-md border border-border">
                  <h3 className={sectionHeadClass}><span className="h-2 w-2 rounded-full bg-sky-500" />Channels</h3>
                  <div className="divide-y divide-border">
                    <div className={rowClass}>
                      <span className={labelClass}>Slack</span>
                      <span className="text-xs text-muted-foreground">Ask in chat to set up team messages</span>
                    </div>
                    <div className={rowClass}>
                      <span className={labelClass}><Mail className="h-3.5 w-3.5 text-violet-500" />Gmail</span>
                      <span className={chipMuted}>Account default From: {gmailSender || 'none'}</span>
                      {!gmailReady && !gmailChecking && info.gmail?.summary && <span className={chipWarn}>{info.gmail.summary}</span>}
                      <span className="ml-auto flex items-center gap-2">
                        <span className={`rounded-full border px-2 py-0.5 text-xs ${gmailReady ? 'border-emerald-500/30 bg-emerald-500/10 text-emerald-700 dark:text-emerald-300' : 'border-border bg-background text-muted-foreground'}`}>
                          {gmailChecking ? 'Checking…' : gmailReady ? 'Connected' : 'Not connected'}
                        </span>
                      </span>
                    </div>
                    <div className={rowClass}>
                      <label htmlFor="workflow-notify-sender" className={labelClass}><Mail className="h-3.5 w-3.5 text-violet-500" />Notify From</label>
                      <select
                        id="workflow-notify-sender"
                        aria-label="Notify Gmail sender"
                        value={senderSelection}
                        onChange={event => { void saveSender(event.target.value) }}
                        disabled={!workspacePath || !canWriteWorkflow || senderSaving}
                        title={!canWriteWorkflow ? READ_ONLY_TITLE : undefined}
                        className="min-w-0 max-w-full rounded-md border border-border bg-background px-2.5 py-1.5 text-xs text-foreground disabled:cursor-not-allowed disabled:opacity-50"
                      >
                        <option value="">Use account default{gmailSender ? ` — ${gmailSender}` : ' — none configured'}</option>
                        {senderSelection && !gmailSenderChoices.some(choice => choice.id === senderSelection) &&
                          <option value={senderSelection} disabled>Unavailable: {senderSelection}</option>}
                        {gmailSenderChoices.map(choice => (
                          <option key={choice.id} value={choice.id} disabled={!choice.ready}>
                            {choice.email || choice.display_name || choice.id}{choice.ready ? '' : ' — unavailable'}
                          </option>
                        ))}
                      </select>
                      {senderSaving && <Loader2 className="h-3.5 w-3.5 animate-spin text-muted-foreground" aria-label="Saving sender" />}
                      <span className="text-xs text-muted-foreground">One account for Notify. Workflow and Builder Gmail actions choose independently.</span>
                      {senderError && <span role="alert" className="text-xs text-destructive">{senderError}</span>}
                    </div>
                    {overrides.length > 0 && (
                      <div className={rowClass}>
                        <span className={labelClass}><Ban className="h-3.5 w-3.5 text-amber-500" />Overrides</span>
                        {overrides.map(item => <span key={item.key} className={chipWarn}>{item.text}</span>)}
                      </div>
                    )}
                  </div>
                </section>

                <section className="rounded-md border border-border">
                  <h3 className={sectionHeadClass}><span className="h-2 w-2 rounded-full bg-violet-500" />What gets sent</h3>
                  <div className="divide-y divide-border">
                    {summaries.map(summary => (
                      <div key={summary.key} className="space-y-2 px-4 py-2.5">
                        <div className="flex items-center justify-between gap-3">
                          <span className="text-xs font-medium text-foreground">{summary.title}</span>
                        </div>
                        <div className="space-y-1">
                          <div className="flex flex-wrap items-center gap-1.5">
                            <span className="w-16 shrink-0 text-[11px] text-muted-foreground">Channels</span>
                            {summary.channels.length > 0
                              ? summary.channels.map(channel => <span key={channel} className={`${chipChannel} capitalize`}>{channel}</span>)
                              : <span className={chipChannel}>All channels</span>}
                          </div>
                          {summary.senders.length > 0 && (
                            <div className="flex flex-wrap items-center gap-1.5">
                              <span className="w-16 shrink-0 text-[11px] text-muted-foreground">From</span>
                              {summary.senders.map(sender => <span key={sender} className={`${chipFrom} font-mono`}>{sender}</span>)}
                            </div>
                          )}
                          {summary.recipients.length > 0 && (
                            <div className="flex flex-wrap items-center gap-1.5">
                              <span className="w-16 shrink-0 text-[11px] text-muted-foreground">To</span>
                              {summary.recipients.length > 0
                                ? summary.recipients.map(email => <span key={email} className={`${chipTo} font-mono`}>{email}</span>)
                                : <span className={chipMuted}>Summary emails are off</span>}
                            </div>
                          )}
                        </div>
                        {summary.instructions && (
                          <NotificationInstructions target={'run_summary'} title={summary.title} instructions={summary.instructions} />
                        )}
                      </div>
                    ))}
                  </div>
                </section>
              </div>
            ) : null}
          </div>
        </div>
  )
}
