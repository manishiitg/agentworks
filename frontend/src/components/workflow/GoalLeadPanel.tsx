import { useCallback, useEffect, useState } from 'react'
import { Check, Loader2, MessageSquare, Pencil, ScrollText, Send, UserRound } from 'lucide-react'
import { agentApi } from '../../services/api'
import type { PulseDecisionLogEntry, PulseFocusArea, PulseRecommendation, ReportHumanInput } from '../../services/api-types'
import { useChatStore } from '../../stores/useChatStore'
import { useLiveRefetch } from '../../hooks/useLiveRefetch'
import { openReportHumanInputAnswerInChat } from '../../utils/reportHumanInputChat'
import { sendWorkspacePaneMessageToChat } from '../../utils/workspacePaneChat'
import { openPulseChatTab } from '../../utils/pulseChatTab'
import { FocusAreasCard } from './GoalLeadConversation'

// The Pulse's part of the Pulse tab (PLAT-697 phase 3), under the goal
// status card: Needs you with the Pulse's recommendation (Accept / Change,
// how long it has waited, what it blocks), the decision log (what it
// recommended, why, what the owner did, what happened after), and the goal
// memory the owner can read and edit. The Pulse recommends; only the
// owner answers. The Pulse's conversation is in its own "<workflow> Pulse"
// chat tab, not here; the "Talk to Pulse" box sends straight into it and opens
// that tab (owner, 2026-10-08). The Builder chat also talks to Pulse
// (ask_pulse).

/** "3 days", "5 hours", "a few minutes" since an ISO time. */
function waitingFor(since: string | undefined, now = Date.now()): string {
  const start = since ? new Date(since).getTime() : NaN
  if (Number.isNaN(start)) return ''
  const hours = Math.max(0, (now - start) / 3_600_000)
  if (hours < 1) return 'under an hour'
  if (hours < 48) return `${Math.floor(hours)} hour${Math.floor(hours) === 1 ? '' : 's'}`
  const days = Math.floor(hours / 24)
  return `${days} days`
}

function shortDate(value?: string): string {
  if (!value) return ''
  const date = new Date(value)
  return Number.isNaN(date.getTime()) ? '' : date.toLocaleDateString(undefined, { month: 'short', day: 'numeric' })
}

function recommendedLabel(input: ReportHumanInput, rec: PulseRecommendation): string {
  return input.options.find(option => option.id === rec.option_id)?.title || rec.option_title || rec.option_id || rec.answer || ''
}

export function NeedsYouCard({ input, workspacePath, onAnswered }: {
  input: ReportHumanInput
  workspacePath: string
  onAnswered?: () => void
}) {
  const [busy, setBusy] = useState(false)
  const [changing, setChanging] = useState(false)
  const rec = input.recommendation
  const waited = waitingFor(input.created_at)

  const answer = async (body: { selected_option_id?: string; note?: string }, label: string) => {
    setBusy(true)
    try {
      const result = await agentApi.answerReportHumanInput(workspacePath, input.id, body)
      if (!result.success) throw new Error(result.error || 'Could not save the answer.')
      // Applying it happens in the Builder chat, where the owner can watch.
      if (result.apply_message) await sendWorkspacePaneMessageToChat({ workspacePath, message: result.apply_message })
      useChatStore.getState().addToast(`"${label}" saved.`, 'success')
      onAnswered?.()
    } catch (err) {
      useChatStore.getState().addToast(err instanceof Error ? err.message : 'Could not save the answer.', 'error')
    } finally {
      setBusy(false)
    }
  }

  const accept = () => {
    if (!rec) return
    const label = recommendedLabel(input, rec)
    void answer(rec.option_id ? { selected_option_id: rec.option_id } : { note: rec.answer }, label)
  }

  const change = () => {
    if (input.options.length === 0) {
      void openReportHumanInputAnswerInChat({ input, workspacePath }).catch(err =>
        useChatStore.getState().addToast(err instanceof Error ? err.message : 'Could not open the chat.', 'error'))
      return
    }
    setChanging(open => !open)
  }

  return <article aria-label={`Decision: ${input.question}`} className="rounded-md border bg-background p-3">
    <div className="flex flex-wrap items-center gap-x-2 gap-y-0.5 text-[11px] text-muted-foreground">
      {waited && <span>Waiting {waited}</span>}
      {rec?.blocks && <span>· Blocks: <span className="text-foreground">{rec.blocks}</span></span>}
    </div>
    <h4 className="mt-1 text-sm font-semibold leading-snug text-foreground">{input.question}</h4>
    {rec ? <div className="mt-2 rounded-md border border-primary/25 bg-primary/5 px-2.5 py-2 text-xs">
      <p><span className="font-semibold text-foreground">Pulse recommends: </span>{recommendedLabel(input, rec)}
        <span className="text-muted-foreground"> · {rec.confidence} confidence</span></p>
      <p className="mt-1 leading-5 text-muted-foreground">{rec.why}</p>
      {rec.evidence && <details className="mt-1"><summary className="cursor-pointer text-[11px] font-medium text-foreground/80">Evidence</summary>
        <p className="mt-1 whitespace-pre-line text-[11px] text-muted-foreground">{rec.evidence}</p></details>}
      {rec.safe_default_by && <p className="mt-1 text-[11px] text-muted-foreground">Safe default if you do not answer: this option by {new Date(rec.safe_default_by).toLocaleString(undefined, { month: 'short', day: 'numeric', hour: 'numeric', minute: '2-digit' })}. The Pulse does not apply it on its own yet.</p>}
    </div> : <p className="mt-2 text-xs text-muted-foreground">No recommendation yet; the next goal check adds one.</p>}
    <div className="mt-2 flex flex-wrap items-center gap-2">
      {rec && <button type="button" onClick={accept} disabled={busy}
        className="inline-flex h-8 items-center gap-1.5 rounded-md border border-primary/40 bg-primary/10 px-3 text-xs font-semibold text-primary hover:bg-primary/15 disabled:opacity-50">
        {busy ? <Loader2 className="h-3.5 w-3.5 animate-spin" /> : <Check className="h-3.5 w-3.5" />}Accept</button>}
      <button type="button" onClick={change} disabled={busy} aria-expanded={input.options.length > 0 ? changing : undefined}
        className="inline-flex h-8 items-center gap-1.5 rounded-md border bg-background px-3 text-xs font-medium text-foreground hover:bg-muted disabled:opacity-50">
        <Pencil className="h-3.5 w-3.5" />{rec ? 'Change' : 'Answer'}</button>
    </div>
    {changing && <div className="mt-2 grid gap-1.5 sm:grid-cols-2">
      {input.options.map(option => <button key={option.id} type="button" disabled={busy}
        onClick={() => void answer({ selected_option_id: option.id }, option.title)}
        className="rounded-md border bg-card/50 p-2 text-left text-xs hover:bg-muted disabled:opacity-50">
        <span className="block font-semibold text-foreground">{option.title}</span>
        {option.description && <span className="mt-0.5 block text-muted-foreground">{option.description}</span>}
      </button>)}
    </div>}
  </article>
}

function ownerText(entry: PulseDecisionLogEntry): string {
  switch (entry.owner_response) {
    case 'accepted': return `You accepted${entry.responded_at ? ` on ${shortDate(entry.responded_at)}` : ''}`
    case 'changed': return `You chose ${entry.owner_answer || 'another option'} instead`
    case 'dismissed': return 'You dismissed it'
    default: return 'Waiting for you'
  }
}

function DecisionLog({ entries }: { entries: PulseDecisionLogEntry[] }) {
  const [all, setAll] = useState(false)
  if (entries.length === 0) return null
  const shown = all ? entries : entries.slice(0, 5)
  return <section aria-label="Decision log" className="rounded-lg border bg-background p-3">
    <div className="flex items-center gap-2"><ScrollText className="h-4 w-4 text-primary" /><h3 className="text-xs font-semibold">What Pulse recommended</h3></div>
    <ul className="mt-1 divide-y">
      {shown.map(entry => <li key={entry.input_id} className="py-2 text-xs">
        <p className="font-medium text-foreground">{entry.question || entry.input_id}</p>
        <p className="mt-0.5 text-muted-foreground"><span className="text-foreground">Recommended {entry.recommended}</span> · {entry.why}</p>
        <p className="mt-0.5 text-[11px] text-muted-foreground">{ownerText(entry)} · {entry.outcome ? <span className="text-foreground">Result: {entry.outcome}</span> : entry.owner_response && entry.owner_response !== 'dismissed' ? 'Result not known yet' : shortDate(entry.recommended_at)}</p>
      </li>)}
    </ul>
    {entries.length > 5 && <button type="button" onClick={() => setAll(value => !value)} className="mt-1 text-[11px] font-medium text-primary">
      {all ? 'Show recent' : `Show all ${entries.length}`}</button>}
  </section>
}

function GoalMemory({ workspacePath, memory, path, onSaved }: { workspacePath: string; memory: string; path: string; onSaved: () => void }) {
  const [draft, setDraft] = useState(memory)
  const [saving, setSaving] = useState(false)
  useEffect(() => { setDraft(memory) }, [memory])
  const entries = memory.split('\n').filter(line => line.trim().startsWith('- ')).length
  const save = async () => {
    setSaving(true)
    try {
      const result = await agentApi.saveGoalMemory(workspacePath, draft)
      if (!result.success) throw new Error(result.error || 'Could not save goal memory.')
      useChatStore.getState().addToast('Goal memory saved.', 'success')
      onSaved()
    } catch (err) {
      useChatStore.getState().addToast(err instanceof Error ? err.message : 'Could not save goal memory.', 'error')
    } finally {
      setSaving(false)
    }
  }
  return <details className="rounded-lg border bg-background px-3 py-2 text-xs">
    <summary className="cursor-pointer font-semibold">Goal memory <span className="font-normal text-muted-foreground">· {entries} entries</span></summary>
    <p className="mt-1 text-[11px] text-muted-foreground">What Pulse remembers about how you want this goal managed: your answers, decisions and results, lessons, open bets. Your goal in soul.md always wins. Stored in {path || 'memory/goal.md'}.</p>
    <textarea aria-label="Goal memory" value={draft} onChange={event => setDraft(event.target.value)} rows={10}
      placeholder="Nothing yet. Your answers to decisions are added here automatically."
      className="mt-2 w-full rounded-md border bg-background p-2 font-mono text-[11px] leading-5" />
    {draft !== memory && <button type="button" onClick={() => void save()} disabled={saving}
      className="mt-1 inline-flex items-center gap-1 rounded-md border border-primary/40 bg-primary/10 px-2 py-1 text-xs font-semibold text-primary disabled:opacity-50">
      {saving && <Loader2 className="h-3 w-3 animate-spin" />}Save</button>}
  </details>
}

function errorText(err: unknown, fallback: string): string {
  const data = (err as { response?: { data?: unknown } })?.response?.data
  if (typeof data === 'string' && data.trim()) return data.trim()
  return err instanceof Error ? err.message : fallback
}

// The owner talks to the Builder and the Builder talks to Pulse (owner,
// 2026-10-08): these questions go to the Builder chat tagged #pulse; the
// Builder asks Pulse (ask_pulse) and shows the reply there.
const PULSE_QUICK_ASKS = [
  { label: 'How is the goal doing?', message: 'How is the goal doing? The key number, its trend, and what is helping or blocking it.' },
  { label: 'What are you working on?', message: 'What are you working on now, and what comes next?' },
  { label: 'What worked so far?', message: 'What have you tried for the goal, what worked and what did not?' },
  { label: 'What would move the goal most?', message: 'What one change would move the goal most this week, and what do you need from me or the Builder for it?' },
  { label: 'What do you need from me?', message: 'What decisions or information do you need from me, with your recommendation for each?' },
]

export function AskPulseViaBuilder({ workspacePath }: { workspacePath: string }) {
  const [sending, setSending] = useState<string | null>(null)
  const ask = async (label: string, message: string) => {
    setSending(label)
    try {
      await sendWorkspacePaneMessageToChat({ workspacePath, message: `#pulse ${message}` })
    } catch (err) {
      useChatStore.getState().addToast(errorText(err, 'Could not send the question.'), 'error')
    } finally {
      setSending(null)
    }
  }
  return <section aria-label="Ask Pulse" className="rounded-lg border bg-background p-2.5">
    <p className="flex items-center gap-1.5 text-[11px] text-muted-foreground"><MessageSquare className="h-3.5 w-3.5 text-primary" />
      Ask Pulse through the Builder chat. For anything else, type #pulse in the Builder chat.</p>
    <div className="mt-2 flex flex-wrap gap-1.5">
      {PULSE_QUICK_ASKS.map(({ label, message }) => <button key={label} type="button" disabled={sending !== null}
        onClick={() => void ask(label, message)}
        className="inline-flex items-center gap-1 rounded-full border border-primary/30 bg-primary/5 px-2.5 py-1 text-[11px] font-medium text-foreground hover:bg-primary/10 disabled:opacity-50">
        {sending === label && <Loader2 className="h-3 w-3 animate-spin" />}{label}</button>)}
    </div>
  </section>
}

/** Direct "Talk to Pulse", kept for testing: the message goes into Pulse's conversation; its reply shows in the "<workflow> Pulse" chat tab. */
export function TalkToPulse({ workspacePath, sessionId }: { workspacePath: string; sessionId: string }) {
  const [draft, setDraft] = useState('')
  const [sending, setSending] = useState(false)
  const send = async () => {
    const message = draft.trim()
    if (!message) return
    setSending(true)
    try {
      const result = await agentApi.sendGoalLeadMessage(workspacePath, message)
      if (!result.success) throw new Error(result.error || 'Could not send the message.')
      setDraft('')
      await openPulseChatTab(workspacePath, sessionId)
    } catch (err) {
      useChatStore.getState().addToast(errorText(err, 'Could not send the message.'), 'error')
    } finally {
      setSending(false)
    }
  }
  return <details aria-label="Talk to Pulse" className="rounded-lg border bg-background px-2 py-1.5">
    <summary className="cursor-pointer text-[11px] text-muted-foreground">Message Pulse directly (testing)</summary>
    <p className="mt-1 px-0.5 text-[11px] text-muted-foreground">Goes straight to Pulse, not through the Builder. Replies appear in the Pulse chat tab.</p>
    <div className="mt-1.5 flex items-end gap-2">
      <textarea aria-label="Message to Pulse" value={draft} onChange={event => setDraft(event.target.value)} rows={1}
        placeholder="Why did you pause the growth runs? / Focus on new subscribers this month."
        onKeyDown={event => { if (event.key === 'Enter' && !event.shiftKey && !event.nativeEvent.isComposing) { event.preventDefault(); void send() } }}
        className="min-w-0 flex-1 resize-none rounded-md border bg-background px-2 py-1.5 text-xs leading-5" />
      <button type="button" onClick={() => void send()} disabled={sending || !draft.trim()}
        className="inline-flex h-8 items-center gap-1.5 rounded-md border border-primary/40 bg-primary/10 px-3 text-xs font-semibold text-primary disabled:opacity-50">
        {sending ? <Loader2 className="h-3.5 w-3.5 animate-spin" /> : <Send className="h-3.5 w-3.5" />}Send</button>
    </div>
  </details>
}

export function GoalLeadPanel({ workspacePath }: { workspacePath: string }) {
  const [pending, setPending] = useState<ReportHumanInput[]>([])
  const [log, setLog] = useState<PulseDecisionLogEntry[]>([])
  const [memory, setMemory] = useState('')
  const [memoryPath, setMemoryPath] = useState('')
  const [hasGoal, setHasGoal] = useState(false)
  const [pulseSession, setPulseSession] = useState('')
  const [focusAreas, setFocusAreas] = useState<PulseFocusArea[]>([])

  const load = useCallback(async () => {
    const [inputs, lead] = await Promise.allSettled([
      agentApi.listReportHumanInputs(workspacePath, 'pending'),
      agentApi.getGoalLead(workspacePath),
    ])
    if (inputs.status === 'fulfilled' && inputs.value.success) {
      setPending((inputs.value.inputs || []).filter(input => input.source !== 'user_suggestion'))
    }
    if (lead.status === 'fulfilled' && lead.value.success) {
      setLog(lead.value.decision_log || [])
      setMemory(lead.value.memory || '')
      setMemoryPath(lead.value.memory_path || '')
      setHasGoal(Boolean(lead.value.conversation?.has_goal))
      setPulseSession(lead.value.conversation?.session_id || '')
      setFocusAreas(lead.value.focus_areas || [])
    }
  }, [workspacePath])

  useEffect(() => { void load() }, [load])
  useLiveRefetch(() => { void load() }, { kinds: ['human_inputs'], workflow: workspacePath, fallbackMs: 0, safetyMs: 0 })

  return <div className="mb-3 space-y-3" aria-label="Pulse">
    <FocusAreasCard workspacePath={workspacePath} areas={focusAreas} onChanged={() => void load()} />
    {pending.length > 0 && <section aria-label="Needs you" className="rounded-lg border border-amber-500/30 bg-amber-500/5 p-3">
      <div className="flex items-center gap-2"><UserRound className="h-4 w-4 text-amber-600 dark:text-amber-300" />
        <h3 className="text-xs font-semibold">Needs you</h3>
        <span className="rounded-full bg-muted px-2 py-0.5 text-[10px] font-semibold tabular-nums text-muted-foreground">{pending.length}</span></div>
      <div className="mt-2 space-y-2">
        {pending.map(input => <NeedsYouCard key={input.id} input={input} workspacePath={workspacePath} onAnswered={() => void load()} />)}
      </div>
    </section>}
    <DecisionLog entries={log} />
    <GoalMemory workspacePath={workspacePath} memory={memory} path={memoryPath} onSaved={() => void load()} />
    {hasGoal && <AskPulseViaBuilder workspacePath={workspacePath} />}
    {hasGoal && <TalkToPulse workspacePath={workspacePath} sessionId={pulseSession} />}
  </div>
}
