import { useState } from 'react'
import { Check, Crosshair, Loader2, MessageSquare, Send, X } from 'lucide-react'
import { agentApi } from '../../services/api'
import type { GoalLeadConversation, GoalLeadMessage, PulseFocusArea } from '../../services/api-types'
import { useChatStore } from '../../stores/useChatStore'

// The Goal Lead as its own chat kind in the Pulse tab (PLAT-697 phase 4): its
// focus areas (the Goal Lead proposes, the owner confirms with one click) and
// its one persistent conversation, where the owner asks "why did you…" or
// gives direction. Lasting direction goes to goal memory; goal changes come
// back as proposed soul.md edits.

function shortDate(value?: string): string {
  if (!value) return ''
  const date = new Date(value.length === 10 ? `${value}T00:00:00` : value)
  return Number.isNaN(date.getTime()) ? '' : date.toLocaleDateString(undefined, { month: 'short', day: 'numeric' })
}

function errorText(err: unknown, fallback: string): string {
  const data = (err as { response?: { data?: unknown } })?.response?.data
  if (typeof data === 'string' && data.trim()) return data.trim()
  return err instanceof Error ? err.message : fallback
}

const progressLabel: Record<string, string> = { moving: 'Moving', stuck: 'Stuck', done: 'Done' }

export function FocusAreasCard({ workspacePath, areas, onChanged }: {
  workspacePath: string
  areas: PulseFocusArea[]
  onChanged: () => void
}) {
  const [busy, setBusy] = useState('')
  const open = areas.filter(area => area.status === 'active' || area.status === 'proposed')
  if (open.length === 0) return null

  const act = async (area: PulseFocusArea, action: 'confirm' | 'reject' | 'close') => {
    setBusy(`${action}:${area.id}`)
    try {
      const result = await agentApi.updateGoalLeadFocusArea(workspacePath, action === 'close' ? { action, id: area.id, status: 'dropped' } : { action, id: area.id })
      if (!result.success) throw new Error(result.error || 'Could not update the focus area.')
      onChanged()
    } catch (err) {
      useChatStore.getState().addToast(errorText(err, 'Could not update the focus area.'), 'error')
    } finally {
      setBusy('')
    }
  }

  return <section aria-label="Focus areas" className="rounded-lg border bg-background p-3">
    <div className="flex items-center gap-2"><Crosshair className="h-4 w-4 text-primary" /><h3 className="text-xs font-semibold">Focus areas</h3>
      <span className="text-[11px] text-muted-foreground">What matters now</span></div>
    <ul className="mt-2 space-y-2">
      {open.map(area => <li key={area.id} aria-label={`Focus area: ${area.text}`} className={`rounded-md border p-2 text-xs ${area.status === 'proposed' ? 'border-dashed border-primary/40 bg-primary/5' : 'bg-card/50'}`}>
        <div className="flex flex-wrap items-start justify-between gap-2">
          <div className="min-w-0">
            <p className="font-medium text-foreground">{area.text}</p>
            <p className="mt-0.5 text-[11px] text-muted-foreground">
              {area.status === 'proposed' ? 'Goal Lead proposes' : 'Active'}
              {area.end_date && <> · until {shortDate(area.end_date)}</>}
              {area.check && <> · check: {area.check}</>}
            </p>
            {area.status === 'proposed' && area.why && <p className="mt-0.5 text-[11px] text-muted-foreground">{area.why}</p>}
            {area.status === 'active' && area.progress && <p className="mt-0.5 text-[11px]">
              <span className="font-semibold text-foreground">{progressLabel[area.progress] || area.progress}</span>
              {area.progress_note && <span className="text-muted-foreground"> · {area.progress_note}</span>}
            </p>}
          </div>
          {area.status === 'proposed' ? <div className="flex shrink-0 gap-1.5">
            <button type="button" onClick={() => void act(area, 'confirm')} disabled={busy !== ''}
              className="inline-flex h-7 items-center gap-1 rounded-md border border-primary/40 bg-primary/10 px-2 text-[11px] font-semibold text-primary disabled:opacity-50">
              {busy === `confirm:${area.id}` ? <Loader2 className="h-3 w-3 animate-spin" /> : <Check className="h-3 w-3" />}Confirm</button>
            <button type="button" onClick={() => void act(area, 'reject')} disabled={busy !== ''}
              className="inline-flex h-7 items-center gap-1 rounded-md border bg-background px-2 text-[11px] text-foreground disabled:opacity-50">
              <X className="h-3 w-3" />Reject</button>
          </div> : <button type="button" onClick={() => void act(area, 'close')} disabled={busy !== ''} aria-label={`Drop focus area ${area.text}`}
            className="shrink-0 rounded-md px-1.5 py-1 text-[11px] text-muted-foreground hover:bg-muted disabled:opacity-50">Drop</button>}
        </div>
      </li>)}
    </ul>
  </section>
}

const roleLabel = (message: GoalLeadMessage): string => {
  switch (message.role) {
    case 'owner': return message.source || 'You'
    case 'slack': return message.source || 'Slack'
    case 'ask': return `${message.source || 'A workflow chat'} asked`
    case 'check': return 'Goal check'
    case 'goal_work': return 'Goal Work'
    case 'qa': return 'QA run'
    case 'system': return 'Note'
    default: return 'Goal Lead'
  }
}

export function GoalLeadChat({ workspacePath, conversation, onSent }: {
  workspacePath: string
  conversation: GoalLeadConversation | null
  onSent: () => void
}) {
  const [draft, setDraft] = useState('')
  const [sending, setSending] = useState(false)
  if (!conversation?.has_goal) return null
  const messages = conversation.messages || []

  const send = async () => {
    const message = draft.trim()
    if (!message) return
    setSending(true)
    try {
      const result = await agentApi.sendGoalLeadMessage(workspacePath, message)
      if (!result.success) throw new Error(result.error || 'Could not send the message.')
      setDraft('')
      onSent()
    } catch (err) {
      useChatStore.getState().addToast(errorText(err, 'Could not send the message.'), 'error')
    } finally {
      setSending(false)
    }
  }

  return <section aria-label="Goal Lead conversation" className="rounded-lg border bg-background p-3">
    <div className="flex items-center gap-2"><MessageSquare className="h-4 w-4 text-primary" /><h3 className="text-xs font-semibold">Talk to the Goal Lead</h3>
      {conversation.busy && <span className="inline-flex items-center gap-1 text-[11px] text-muted-foreground"><Loader2 className="h-3 w-3 animate-spin" />working</span>}</div>
    <p className="mt-0.5 text-[11px] text-muted-foreground">Ask why it did something or give direction. Direction that should last goes to its memory; a change to the goal comes back as a proposed edit to soul.md.</p>
    {messages.length > 0 && <ol className="mt-2 max-h-80 space-y-1.5 overflow-y-auto">
      {messages.map(message => <li key={message.id} className={`rounded-md px-2.5 py-1.5 text-xs ${message.role === 'owner' || message.role === 'slack' ? 'ml-6 bg-primary/10' : 'mr-6 bg-muted/60'}`}>
        <p className="text-[10px] font-semibold text-muted-foreground">{roleLabel(message)} · {shortDate(message.at)}</p>
        <p className="mt-0.5 whitespace-pre-line leading-5 text-foreground">{message.text}</p>
      </li>)}
    </ol>}
    <div className="mt-2 flex items-end gap-2">
      <textarea aria-label="Message to the Goal Lead" value={draft} onChange={event => setDraft(event.target.value)} rows={2}
        placeholder="Why did you pause the growth runs? / Focus on new subscribers this month."
        onKeyDown={event => { if (event.key === 'Enter' && (event.metaKey || event.ctrlKey)) void send() }}
        className="min-w-0 flex-1 rounded-md border bg-background p-2 text-xs leading-5" />
      <button type="button" onClick={() => void send()} disabled={sending || !draft.trim()}
        className="inline-flex h-8 items-center gap-1.5 rounded-md border border-primary/40 bg-primary/10 px-3 text-xs font-semibold text-primary disabled:opacity-50">
        {sending ? <Loader2 className="h-3.5 w-3.5 animate-spin" /> : <Send className="h-3.5 w-3.5" />}Send</button>
    </div>
  </section>
}
