import { useCallback, useEffect, useRef, useState } from 'react'
import api from '../../services/api'
import { useLiveRefetch } from '../../hooks/useLiveRefetch'

/** A workflow run problem card as the server words it (run_problems.go, #3617): missed or failed, with its buttons. */
export type RunProblemCard = { id: string; kind: 'missed' | 'failed'; title: string; line: string; at: string; buttons: string[] }
type RunProblemReply = { ok: boolean; note?: string; ask?: string }

/** The owner's run-problem cards for one Crew; refreshed on live updates, focus, and after each button. */
export function useRunProblems(projectId?: string, enabled = true) {
  const [state, setState] = useState<{ projectId: string; cards: RunProblemCard[] }>()
  const loadRef = useRef<() => Promise<void>>(async () => {})
  useEffect(() => {
    if (!projectId || !enabled) return
    const controller = new AbortController()
    let reading = false
    const load = async () => {
      if (reading || controller.signal.aborted) return
      reading = true
      try {
        const response = await api.get<{ cards: RunProblemCard[] }>(`/api/crew-run-problems/${encodeURIComponent(projectId)}`, { signal: controller.signal, skipSessionContext: true })
        if (!controller.signal.aborted) setState({ projectId, cards: response.data.cards || [] })
      } catch {
        if (!controller.signal.aborted) setState({ projectId, cards: [] })
      } finally {
        reading = false
      }
    }
    loadRef.current = load
    void load()
    return () => { controller.abort(); loadRef.current = async () => {} }
  }, [projectId, enabled])
  useLiveRefetch(() => { void loadRef.current() }, {
    kinds: ['crew_panel', 'schedules'],
    project: projectId,
    enabled: enabled && Boolean(projectId),
    refreshOnFocus: true,
    fallbackMs: 60_000,
    safetyMs: 60_000,
    minIntervalMs: 3_000,
  })
  const press = useCallback(async (card: RunProblemCard, button: string): Promise<RunProblemReply> => {
    const response = await api.post<RunProblemReply>(`/api/crew-run-problems/${encodeURIComponent(projectId || '')}/${encodeURIComponent(card.id)}`, { button })
    await loadRef.current()
    return response.data
  }, [projectId])
  return { cards: state && state.projectId === projectId ? state.cards : [], press }
}

/** One run-problem card in the Needs-you box. Resume and Ask the agent hand a message to the Crew's agent. */
export function RunProblemCardView({ card, press, onAsk }: { card: RunProblemCard; press: (card: RunProblemCard, button: string) => Promise<RunProblemReply>; onAsk?: (message: string) => void }) {
  const [busy, setBusy] = useState('')
  const [note, setNote] = useState('')
  const click = async (button: string) => {
    setBusy(button); setNote('')
    try {
      const reply = await press(card, button)
      if (reply.ask) {
        if (onAsk) onAsk(reply.ask)
        else setNote(`Ask the project's agent: "${reply.ask}"`)
      } else if (reply.note) setNote(reply.note)
    } catch (e) {
      const message = (e as { response?: { data?: { message?: string; error?: string } } })?.response?.data
      setNote(message?.message || message?.error || 'That did not work. Try again.')
    } finally { setBusy('') }
  }
  return <li className="px-2.5 py-2 text-xs">
    <div className="text-[11px] text-muted-foreground">{card.kind === 'missed' ? 'Missed run' : 'Failed run'}</div>
    <p className="mt-1 break-words text-[13px] font-semibold leading-snug text-foreground">{card.line}</p>
    <div className="mt-2 flex flex-wrap gap-1.5">{card.buttons.map((button, i) => <button key={button} type="button" disabled={!!busy} onClick={() => void click(button)} className={`rounded-md px-2.5 py-1 text-xs font-medium disabled:opacity-50 ${i === 0 ? 'bg-primary text-primary-foreground hover:bg-primary/90' : 'border border-border bg-background hover:bg-muted'}`}>{busy === button ? 'Sending…' : button}</button>)}</div>
    {note && <p className="mt-1 text-[11px] text-muted-foreground">{note}</p>}
  </li>
}
