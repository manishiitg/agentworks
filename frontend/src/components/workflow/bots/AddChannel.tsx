import type { ReactNode } from 'react'
import { ChevronRight, Loader2, MessageSquare, Phone, Plus } from 'lucide-react'
import { READ_ONLY_TITLE } from '../../../hooks/useCanWriteWorkflow'
import { routeId, type ChannelKind } from './types'
import type { WorkflowBots } from './useWorkflowBots'

// One "Add a channel" row: connection status, Set up/Settings drill-in, and
// the inline add form once the connector is ready.

type ChannelRowBots = Pick<WorkflowBots,
  | 'readOnly' | 'workflowId' | 'setSetup'
  | 'slackReady' | 'waReady' | 'slackStatusLabel' | 'waStatusLabel' | 'slackLoading' | 'slackOriginal' | 'waStatus' | 'waError'
  | 'newSlackChannel' | 'setNewSlackChannel' | 'addSlackRoute'
  | 'routeSaving' | 'myRoutes' | 'addError' | 'setAddError'
>

export function ChannelRow({ bots, kind, manageRoutes = false, headerAction }: { bots: ChannelRowBots; kind: ChannelKind; manageRoutes?: boolean; headerAction?: ReactNode }) {
  const {
    readOnly, workflowId, setSetup,
    slackReady, waReady, slackStatusLabel, waStatusLabel, slackLoading, slackOriginal, waStatus, waError,
    newSlackChannel, setNewSlackChannel, addSlackRoute,
    routeSaving, myRoutes, addError, setAddError,
  } = bots

  const ready = kind === 'slack' ? slackReady : waReady
  const statusLabel = kind === 'slack' ? slackStatusLabel : waStatusLabel
  const connected = kind === 'slack' ? statusLabel === 'Connected' : ready
  const loading = kind === 'slack' ? slackLoading : (!waStatus && !waError)
  const Icon = kind === 'slack' ? MessageSquare : Phone
  const name = kind === 'slack' ? 'Slack' : 'WhatsApp'
  const value = newSlackChannel
  const setValue = setNewSlackChannel
  const add = addSlackRoute
  const adding = routeSaving?.startsWith(`${kind}:`) && !myRoutes.some(r => routeId(r) === routeSaving)
  const error = addError[kind]
  return (
    <div className="px-3 py-2.5">
      <div className="flex items-center gap-2">
        <Icon className="h-4 w-4 shrink-0 text-muted-foreground" />
        <span className="text-sm font-medium text-foreground">{name}</span>
        <span
          className={`inline-flex items-center gap-1 text-[11px] font-medium ${connected ? 'text-emerald-600 dark:text-emerald-400' : loading ? 'text-muted-foreground' : 'text-amber-600 dark:text-amber-400'}`}
          title={kind === 'slack' && slackOriginal.enabled && !slackOriginal.bot_mode ? 'Enable the Slack bot in Settings before it can receive messages.' : undefined}
        >
          <span className={`h-1.5 w-1.5 rounded-full ${connected ? 'bg-emerald-500' : loading ? 'bg-muted-foreground/40' : 'bg-amber-500'}`} />
          {statusLabel}
        </span>
        <span className="flex-1" />
        {headerAction}
        {kind !== 'slack' && (
          <button
            type="button"
            onClick={() => setSetup(kind)}
            className="inline-flex items-center gap-1 text-xs font-medium text-muted-foreground transition-colors hover:text-foreground"
            title={`Connect or configure ${name}`}
          >
            Open
            <ChevronRight className="h-3.5 w-3.5" />
          </button>
        )}
      </div>
      {kind === 'slack' && manageRoutes && ready && workflowId && (
        <div className="mt-2 flex items-center gap-2">
          <input
            type="text"
            value={value}
            onChange={e => {
              const next = e.target.value.toUpperCase().replace(/[^A-Z0-9_,;\s-]/g, '')
              setValue(next)
              if (error) setAddError(prev => ({ ...prev, [kind]: undefined }))
            }}
            onKeyDown={e => { if (e.key === 'Enter') add() }}
            placeholder="channel ID, e.g. C1234567890"
            disabled={readOnly || !!adding}
            title={readOnly ? READ_ONLY_TITLE : undefined}
            className="min-w-0 flex-1 px-2 py-1 text-xs bg-secondary border border-border rounded font-mono focus:outline-none focus:ring-1 focus:ring-primary disabled:opacity-50"
          />
          <button
            type="button"
            onClick={add}
            disabled={readOnly || !value.trim() || !!adding}
            title={readOnly ? READ_ONLY_TITLE : undefined}
            className="inline-flex items-center gap-1 rounded-md border border-border px-2 py-1 text-xs font-medium text-foreground transition-colors hover:bg-muted disabled:cursor-not-allowed disabled:opacity-50"
          >
            {adding ? <Loader2 className="h-3.5 w-3.5 animate-spin" /> : <Plus className="h-3.5 w-3.5" />}
            Add
          </button>
        </div>
      )}
      {manageRoutes && error && <p className="mt-1.5 text-xs text-red-600 dark:text-red-400">{error}</p>}
    </div>
  )
}
