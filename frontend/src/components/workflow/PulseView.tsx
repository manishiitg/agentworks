import { useEffect, useRef, useState, type ReactNode } from 'react'
import { useWorkspaceViewTarget } from '../../hooks/useWorkspaceViewTarget'
import { PulseIcon } from './pulseIcon'
import { SoulViewer, WORKFLOW_SOUL_REFRESH_EVENT } from './SoulViewer'
import { GoalProgress } from './GoalProgress'
import { WorkspaceViewHeader } from './WorkspaceViewHeader'
import { WorkspaceViewIconButton } from './WorkspaceViewIconButton'
import { askWorkflowPulse, openWorkflowPulseChatTab } from '../../utils/pulseChatTab'
import { useWorkflowStore } from '../../stores/useWorkflowStore'
import { useChatStore } from '../../stores/useChatStore'
import type { PulseOutwardActions, PulseOverviewResponse } from '../../services/api-types'

export interface PulseViewProps {
  workspacePath: string | null
  monitorOn: boolean
  monitorSaving: boolean
  onToggleMonitor: () => void
  hasSoul?: boolean
  overview?: PulseOverviewResponse | null
  outwardActions?: PulseOutwardActions
  outwardSaving?: boolean
  onChangeOutward?: (next: PulseOutwardActions) => void
  statusError: string | null
  statusLoading: boolean
  onRefresh: () => void
  headerAction?: ReactNode
}


export default function PulseView({ workspacePath, monitorOn, monitorSaving, onToggleMonitor, hasSoul = false,
  overview = null, outwardActions = 'ask', outwardSaving = false,
  onChangeOutward, statusError, statusLoading, onRefresh, headerAction }: PulseViewProps) {
  const settingsSection = useRef<HTMLElement>(null)
  const goalSection = useRef<HTMLElement>(null)
  const metricsSection = useRef<HTMLElement>(null)
  const [target, setTarget] = useState(() => {
    const current = useWorkflowStore.getState().workspaceViewTarget
    return current?.view === 'pulse' ? current.target : 'goals'
  })
  const settingsOnly = target === 'settings'
  const [question, setQuestion] = useState('')
  const [sending, setSending] = useState(false)
  const canTalk = Boolean(monitorOn && hasSoul && workspacePath)
  const fail = (error: unknown) => useChatStore.getState().addToast(error instanceof Error ? error.message : 'Could not open Pulse chat.', 'error')
  const talk = () => { if (workspacePath) void openWorkflowPulseChatTab(workspacePath).catch(fail) }
  const ask = async (message: string) => {
    if (!canTalk || !workspacePath || sending || !message.trim()) return
    setSending(true)
    try {
      await askWorkflowPulse(workspacePath, message)
      if (message === question) setQuestion('')
    } catch (error) { fail(error) }
    finally { setSending(false) }
  }
  useWorkspaceViewTarget('pulse', next => {
    if (next === 'chat') { talk(); return }
    if (next === 'decisions') { useWorkflowStore.getState().openWorkspaceView('human-actions'); return }
    setTarget(next)
  })
  useEffect(() => {
    const section = settingsOnly ? settingsSection.current : target === 'metrics' ? metricsSection.current : goalSection.current
    section?.scrollIntoView({ block: 'start' })
    section?.focus({ preventScroll: true })
  }, [target, settingsOnly])
  return <div className="flex h-full min-h-0 w-full flex-col bg-background">
    <WorkspaceViewHeader icon={PulseIcon} title={settingsOnly ? 'Pulse settings' : 'Goals & metrics'} helpTopic={settingsOnly ? 'Pulse settings' : 'Pulse'} subtitle={settingsOnly ? 'Choose when Pulse works and how it handles outward actions.' : 'Pulse works with Builder on your goal.'}
      actions={<>{headerAction}<WorkspaceViewIconButton label="Refresh Pulse" disabled={statusLoading} spinning={statusLoading}
        onClick={() => { window.dispatchEvent(new CustomEvent(WORKFLOW_SOUL_REFRESH_EVENT)); onRefresh() }} /></>} />
    <div className="min-h-0 flex-1 overflow-y-auto"><div className="mx-auto max-w-4xl space-y-4 p-4 sm:p-6">
      {statusError && <p role="alert" className="rounded-lg border border-destructive/30 p-3 text-sm text-destructive">{statusError}</p>}
      {!settingsOnly && <>
      <section ref={goalSection} tabIndex={-1} aria-label="Goals" className="space-y-3 focus:outline-none">
        <div className="flex flex-wrap items-center justify-between gap-2"><h2 className="text-sm font-semibold">Your goals</h2>
          <button type="button" disabled={!canTalk || sending} onClick={() => void ask('Review our goals and acceptance conditions. Where are we on track, what is blocking progress, and what should we do next?')}
            className="text-xs font-medium text-primary disabled:opacity-50">Ask Pulse about our goals</button></div>
        {workspacePath && <SoulViewer key={workspacePath} workspacePath={workspacePath} pulseSummary />}
      </section>
      <section ref={metricsSection} tabIndex={-1} aria-label="Metrics" className="focus:outline-none">
        {overview?.measurements_error ? <p role="alert" className="rounded-lg border p-4 text-sm text-destructive">{overview.measurements_error}</p>
          : overview?.measurements && workspacePath ? <GoalProgress impact={overview.measurements} workspacePath={workspacePath} onAskPulse={canTalk && !sending ? message => { void ask(message) } : undefined} />
            : <p className="text-sm text-muted-foreground">{statusLoading ? 'Loading saved goal measurements…' : 'Refresh Pulse to load goal measurements.'}</p>}
      </section>
      </>}
      {settingsOnly && <section ref={settingsSection} tabIndex={-1} aria-label="Pulse settings" className="focus:outline-none rounded-xl border bg-background p-4">
        <div className="flex items-center justify-between gap-4"><div><h2 className="text-sm font-semibold">Pulse</h2>
          <p className="mt-1 text-xs text-muted-foreground">{monitorOn ? 'Checks the goal and works with Builder on improvements.' : 'Fully off. No checks, reports, reminders or messages.'}</p></div>
          <button type="button" role="switch" aria-label="Pulse on/off" aria-checked={monitorOn} disabled={monitorSaving} onClick={onToggleMonitor}
            className={`shrink-0 rounded-full px-4 py-2 text-sm font-medium disabled:opacity-50 ${monitorOn ? 'bg-primary text-primary-foreground' : 'bg-muted text-muted-foreground'}`}>{monitorOn ? 'On' : 'Off'}</button></div>
        <label className="mt-4 flex flex-wrap items-center justify-between gap-3 border-t pt-4 text-sm"><span>Outward actions<span className="block text-xs text-muted-foreground">Posts and messages within your existing limits</span></span>
          <select aria-label="Pulse outward actions" value={outwardActions} disabled={outwardSaving || !onChangeOutward} onChange={event => onChangeOutward?.(event.target.value as PulseOutwardActions)} className="rounded-md border bg-background px-3 py-2 text-sm">
            <option value="ask">Ask first</option><option value="allow">Allowed within limits</option></select></label>
        {monitorOn && !hasSoul && <p className="mt-3 text-sm text-muted-foreground">Set the goal with Builder so Pulse knows what to pursue.</p>}
      </section>}
      {!settingsOnly && <section tabIndex={-1} aria-label="Talk to Pulse" className="focus:outline-none rounded-xl border bg-background p-4">
        <div className="flex flex-wrap items-center justify-between gap-2"><h2 className="text-sm font-semibold">Talk to Pulse</h2>
          <button type="button" disabled={!canTalk} onClick={talk} className="text-sm font-medium text-primary disabled:opacity-50">Open Pulse chat</button></div>
        <p className="mt-1 text-xs text-muted-foreground">{monitorOn ? hasSoul ? 'Send a question to Pulse, or continue in its chat.' : 'Set a goal with Builder before messaging Pulse.' : 'Turn Pulse on before sending it a message.'}</p>
        <div className="mt-3 flex flex-wrap gap-2">{[
          ['Goal progress', 'How is the goal doing? Check the key numbers, their trends, and what is helping or blocking progress, using current evidence.'],
          ['Current work', 'What are you working on now, and what comes next? Check the actual work in progress.'],
          ['What worked?', 'What have you tried for the goal, what worked and what did not? Use the saved evidence and say where the result is unknown.'],
          ['Biggest opportunity', 'What one change would move the goal most this week, and what do you need from me or Builder for it?'],
          ['Needs my input', 'What decisions or information do you need from me, with your recommendation for each?'],
        ].map(([label, message]) => <button key={label} type="button" disabled={!canTalk || sending} onClick={() => void ask(message)} className="rounded-md border px-3 py-2 text-xs hover:bg-muted/40 disabled:opacity-50">{label}</button>)}</div>
        <form className="mt-3 flex flex-col items-end gap-2" onSubmit={event => { event.preventDefault(); void ask(question) }}>
          <textarea aria-label="Question for Pulse" placeholder="Ask Pulse a specific question…" value={question} onChange={event => setQuestion(event.target.value)} rows={2} disabled={!canTalk || sending} className="w-full resize-y rounded-lg border bg-background p-3 text-sm disabled:opacity-50" />
          <button type="submit" disabled={!canTalk || sending || !question.trim()} className="rounded-lg bg-primary px-4 py-2 text-sm font-medium text-primary-foreground disabled:opacity-50">{sending ? 'Opening Pulse…' : 'Send to Pulse'}</button>
        </form>
      </section>}
    </div></div>
  </div>
}
