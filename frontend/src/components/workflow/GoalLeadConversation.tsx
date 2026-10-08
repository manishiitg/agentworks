import { useState } from 'react'
import { Check, Crosshair, Loader2, Plus, X } from 'lucide-react'
import { agentApi } from '../../services/api'
import type { PulseFocusArea } from '../../services/api-types'
import { useChatStore } from '../../stores/useChatStore'

// The Pulse's focus areas in the Pulse tab (PLAT-697 phase 4): the Pulse
// proposes, the owner confirms with one click. Its conversation is not shown
// here: the "<workflow> Pulse" chat tab shows it, and the owner talks to Pulse
// through the Builder chat (owner, 2026-10-08).

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
  const [text, setText] = useState('')
  const [endDate, setEndDate] = useState(() => new Date(Date.now() + 30 * 86_400_000).toISOString().slice(0, 10))
  const open = areas.filter(area => area.status === 'active' || area.status === 'proposed')
  const full = open.length >= 3

  const add = async () => {
    const value = text.trim()
    if (!value) return
    setBusy('add')
    try {
      const result = await agentApi.updateGoalLeadFocusArea(workspacePath, { action: 'add', text: value, end_date: endDate })
      if (!result.success) throw new Error(result.error || 'Could not add the focus area.')
      setText('')
      onChanged()
    } catch (err) {
      useChatStore.getState().addToast(errorText(err, 'Could not add the focus area.'), 'error')
    } finally {
      setBusy('')
    }
  }

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
      <span className="text-[11px] text-muted-foreground">What matters now, up to 3, each with an end date. Pulse looks here first and tracks each on its goal check.</span></div>
    {open.length === 0 && <p className="mt-2 rounded-md border border-dashed px-2.5 py-2 text-[11px] text-muted-foreground">None yet. Pulse picks its own priorities from your goal, and may propose one here for you to confirm.</p>}
    <ul className="mt-2 space-y-2">
      {open.map(area => <li key={area.id} aria-label={`Focus area: ${area.text}`} className={`rounded-md border p-2 text-xs ${area.status === 'proposed' ? 'border-dashed border-primary/40 bg-primary/5' : 'bg-card/50'}`}>
        <div className="flex flex-wrap items-start justify-between gap-2">
          <div className="min-w-0">
            <p className="font-medium text-foreground">{area.text}</p>
            <p className="mt-0.5 text-[11px] text-muted-foreground">
              {area.status === 'proposed' ? 'Pulse proposes' : 'Active'}
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
    {!full && <div className="mt-2 flex flex-wrap items-center gap-2">
      <input value={text} onChange={event => setText(event.target.value)} maxLength={300} disabled={busy !== ''}
        onKeyDown={event => { if (event.key === 'Enter') { event.preventDefault(); void add() } }}
        placeholder="Add one, e.g. Get publishing back to 2 posts a week" aria-label="New focus area"
        className="min-w-0 flex-1 rounded-md border bg-background px-2 py-1 text-xs" />
      <label className="flex items-center gap-1 text-[11px] text-muted-foreground">until
        <input type="date" value={endDate} onChange={event => setEndDate(event.target.value)} aria-label="Focus area end date"
          className="rounded-md border bg-background px-1.5 py-0.5 text-[11px]" /></label>
      <button type="button" onClick={() => void add()} disabled={!text.trim() || busy !== ''}
        className="inline-flex h-7 items-center gap-1 rounded-md border px-2 text-[11px] font-medium hover:bg-muted disabled:opacity-50">
        {busy === 'add' ? <Loader2 className="h-3 w-3 animate-spin" /> : <Plus className="h-3 w-3" />}Add</button>
    </div>}
  </section>
}
