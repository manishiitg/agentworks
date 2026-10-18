import { useCallback, useEffect, useState } from 'react'
import { Check, Loader2, Pencil } from 'lucide-react'
import { agentApi } from '../../services/api'
import type { PulseRecommendation, ReportHumanInput } from '../../services/api-types'
import { useChatStore } from '../../stores/useChatStore'
import { useLiveRefetch } from '../../hooks/useLiveRefetch'
import { openReportHumanInputAnswerInChat } from '../../utils/reportHumanInputChat'
import { sendWorkspacePaneMessageToChat } from '../../utils/workspacePaneChat'

function waitingFor(since: string | undefined, now = Date.now()): string {
  const start = since ? new Date(since).getTime() : NaN
  if (Number.isNaN(start)) return ''
  const hours = Math.max(0, (now - start) / 3_600_000)
  if (hours < 1) return 'under an hour'
  if (hours < 48) return `${Math.floor(hours)} hour${Math.floor(hours) === 1 ? '' : 's'}`
  const days = Math.floor(hours / 24)
  return `${days} days`
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


export function GoalLeadPanel({ workspacePath }: { workspacePath: string }) {
  const [pending, setPending] = useState<ReportHumanInput[]>([])
  const [error, setError] = useState('')
  const load = useCallback(async () => {
    try {
      const result = await agentApi.listReportHumanInputs(workspacePath, 'pending')
      if (!result.success) throw new Error(result.error || 'Could not load your decisions.')
      setPending(result.inputs || [])
      setError('')
    } catch (err) { setError(err instanceof Error ? err.message : 'Could not load your decisions.') }
  }, [workspacePath])
  useEffect(() => { setPending([]); void load() }, [load])
  useLiveRefetch(() => { void load() }, { kinds: ['human_inputs'], workflow: workspacePath, fallbackMs: 0, safetyMs: 0 })
  return <section aria-label="Needs you" className="rounded-xl border bg-background p-4">
    <h2 className="text-sm font-semibold">Needs you</h2>
    {error && <p role="alert" className="mt-2 text-sm text-destructive">{error}</p>}
    {!error && pending.length === 0 && <p className="mt-2 text-sm text-muted-foreground">Nothing is waiting for your answer.</p>}
    <div className="mt-3 space-y-2">{pending.map(input => <NeedsYouCard key={input.id} input={input} workspacePath={workspacePath} onAnswered={() => void load()} />)}</div>
  </section>
}
