import { useRef, type ReactNode } from 'react'
import { useWorkspaceViewTarget } from '../../hooks/useWorkspaceViewTarget'
import { PulseIcon } from './pulseIcon'
import { WORKFLOW_SOUL_REFRESH_EVENT } from './SoulViewer'
import { WorkspaceViewHeader } from './WorkspaceViewHeader'
import { WorkspaceViewIconButton } from './WorkspaceViewIconButton'
import { openWorkflowPulseChatTab } from '../../utils/pulseChatTab'
import { useChatStore } from '../../stores/useChatStore'
import type { PulseOutwardActions } from '../../services/api-types'

export interface PulseViewProps {
  workspacePath: string | null
  monitorOn: boolean
  monitorSaving: boolean
  onToggleMonitor: () => void
  hasSoul?: boolean
  outwardActions?: PulseOutwardActions
  outwardSaving?: boolean
  onChangeOutward?: (next: PulseOutwardActions) => void
  statusError: string | null
  statusLoading: boolean
  onRefresh: () => void
  headerAction?: ReactNode
}


export default function PulseView({ workspacePath, monitorOn, monitorSaving, onToggleMonitor, hasSoul = false,
  outwardActions = 'ask', outwardSaving = false,
  onChangeOutward, statusError, statusLoading, onRefresh, headerAction }: PulseViewProps) {
  const settingsSection = useRef<HTMLElement>(null)
  const chatSection = useRef<HTMLElement>(null)
  useWorkspaceViewTarget('pulse', target => {
    const section = target === 'settings' ? settingsSection.current : target === 'chat' ? chatSection.current : null
    section?.scrollIntoView({ block: 'start' })
    section?.focus({ preventScroll: true })
  })
  return <div className="flex h-full min-h-0 w-full flex-col bg-background">
    <WorkspaceViewHeader icon={PulseIcon} title="Pulse" helpTopic="Pulse" subtitle="Pulse works with Builder on your goal."
      actions={<>{headerAction}<WorkspaceViewIconButton label="Refresh Pulse" disabled={statusLoading} spinning={statusLoading}
        onClick={() => { window.dispatchEvent(new CustomEvent(WORKFLOW_SOUL_REFRESH_EVENT)); onRefresh() }} /></>} />
    <div className="min-h-0 flex-1 overflow-y-auto"><div className="mx-auto max-w-4xl space-y-4 p-4 sm:p-6">
      {statusError && <p role="alert" className="rounded-lg border border-destructive/30 p-3 text-sm text-destructive">{statusError}</p>}
      <section ref={settingsSection} tabIndex={-1} aria-label="Pulse settings" className="focus:outline-none rounded-xl border bg-background p-4">
        <div className="flex items-center justify-between gap-4"><div><h2 className="text-sm font-semibold">Pulse</h2>
          <p className="mt-1 text-xs text-muted-foreground">{monitorOn ? 'Checks the goal and works with Builder on improvements.' : 'Fully off. No checks, reports, reminders or messages.'}</p></div>
          <button type="button" role="switch" aria-label="Pulse on/off" aria-checked={monitorOn} disabled={monitorSaving} onClick={onToggleMonitor}
            className={`shrink-0 rounded-full px-4 py-2 text-sm font-medium disabled:opacity-50 ${monitorOn ? 'bg-primary text-primary-foreground' : 'bg-muted text-muted-foreground'}`}>{monitorOn ? 'On' : 'Off'}</button></div>
        <label className="mt-4 flex flex-wrap items-center justify-between gap-3 border-t pt-4 text-sm"><span>Outward actions<span className="block text-xs text-muted-foreground">Posts and messages within your existing limits</span></span>
          <select aria-label="Pulse outward actions" value={outwardActions} disabled={outwardSaving || !onChangeOutward} onChange={event => onChangeOutward?.(event.target.value as PulseOutwardActions)} className="rounded-md border bg-background px-3 py-2 text-sm">
            <option value="ask">Ask first</option><option value="allow">Allowed within limits</option></select></label>
        {monitorOn && !hasSoul && <p className="mt-3 text-sm text-muted-foreground">Set the goal with Builder so Pulse knows what to pursue.</p>}
      </section>
      <section ref={chatSection} tabIndex={-1} aria-label="Talk to Pulse" className="focus:outline-none rounded-xl border bg-background p-4"><h2 className="text-sm font-semibold">Talk to Pulse</h2>
        <p className="mt-1 text-sm text-muted-foreground">{monitorOn ? 'Ask about progress or give Pulse direction in its chat.' : 'Turn Pulse on before sending it a message.'}</p>
        <button type="button" disabled={!monitorOn || !hasSoul || !workspacePath} className="mt-3 rounded-lg bg-primary px-4 py-2 text-sm font-medium text-primary-foreground disabled:opacity-50"
          onClick={() => { if (workspacePath) void openWorkflowPulseChatTab(workspacePath).catch(err => useChatStore.getState().addToast(err instanceof Error ? err.message : 'Could not open Pulse.', 'error')) }}>Open Pulse chat</button>
      </section>
    </div></div>
  </div>
}
