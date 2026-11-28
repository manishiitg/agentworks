import { useCallback, useEffect, useMemo, useRef, useState, useSyncExternalStore, type ReactNode } from 'react'
import { createPortal } from 'react-dom'
import { ListChecks, X } from 'lucide-react'
import { useChatStore } from '../../stores/useChatStore'
import { currentNextStepsEvent, nextStepsOf, type NextOption, type NextSteps } from '../../utils/nextSteps'

// Summary and next steps (owner, 26 Nov, like Claude Desktop): the agent's last reply ended with a short summary, one
// question and two or three answers. Shown after that reply in the chat, scrolling with it like Claude Desktop's (owner,
// 26 Nov: not pinned to the message box), until the person sends a message or dismisses it; never saved. While the box is empty the first answer also goes with Cmd+Enter (ChatInput); typing only drops that shortcut.

export function useNextSteps(session: string | null, available: boolean) {
  const event = useChatStore(state => session ? currentNextStepsEvent(state.tabEvents[session]) : undefined)
  const steps = useMemo(() => nextStepsOf(event), [event])
  const [dismissed, setDismissed] = useState<string>()
  const shown = available && event && steps && event.id !== dismissed ? steps : undefined
  return { steps: shown, dismiss: () => setDismissed(event?.id) }
}

// Where each chat's card goes: the transcript's footer, inside the scroller, after the last reply.
const slots = new Map<string, HTMLElement>()
const slotListeners = new Set<() => void>()
const notifySlots = () => slotListeners.forEach(listener => listener())
const subscribeSlots = (listener: () => void) => { slotListeners.add(listener); return () => { slotListeners.delete(listener) } }

/** The place in the transcript (its footer, inside the scroller) where this chat's card is shown. */
export function NextStepsSlotHost({ slotId }: { slotId: string }) {
  const mine = useRef<HTMLElement | null>(null)
  const register = useCallback((el: HTMLDivElement | null) => {
    if (el) { mine.current = el; slots.set(slotId, el) }
    else if (mine.current && slots.get(slotId) === mine.current) { slots.delete(slotId); mine.current = null }
    notifySlots()
  }, [slotId])
  return <div ref={register} data-next-steps-slot={slotId} className="px-3 empty:hidden" />
}

/** Shows the card after the last reply (the transcript's slot for this chat); with no slot on screen, above the message box. */
export function NextStepsSlot({ tabId, children }: { tabId: string; children: ReactNode }) {
  const host = useSyncExternalStore(subscribeSlots, () => slots.get(tabId) ?? null)
  return host ? createPortal(children, host) : <>{children}</>
}

const isMac = typeof navigator !== 'undefined' && /Mac|iPhone|iPad/.test(navigator.platform || navigator.userAgent || '')

/** How an answer goes from this chat: as a goal or a plan where the chat's app has one, else as its plain text. */
export function nextOptionMode(option: NextOption, modes: { goal?: boolean; plan?: boolean }): 'goal' | 'plan' | undefined {
  if (option.goal && modes.goal) return 'goal'
  if (option.plan && modes.plan) return 'plan'
}

export function NextStepsCard({ steps, onPick, onDismiss, onTurnOff, onTurnOn, shortcut = true, modes = {} }: { steps: NextSteps; shortcut?: boolean; modes?: { goal?: boolean; plan?: boolean }; onPick: (option: NextOption) => void; onDismiss: () => void; onTurnOff?: () => void; onTurnOn?: () => void }) {
  const ref = useRef<HTMLElement>(null)
  const [off, setOff] = useState(false)
  // It appears as the reply ends; bring it into view the way the reply itself was.
  useEffect(() => { ref.current?.scrollIntoView?.({ block: 'nearest', behavior: 'smooth' }) }, [])
  // Owner, 26 Nov: after Turn off, say briefly where it turns back on (with Undo), then hide.
  const dismissRef = useRef(onDismiss)
  dismissRef.current = onDismiss
  useEffect(() => {
    if (!off) return
    const timer = window.setTimeout(() => dismissRef.current(), 6000)
    return () => window.clearTimeout(timer)
  }, [off])
  if (off) return <p role="status" data-testid="next-steps-off-note" className="my-3 flex flex-wrap items-center gap-x-2 px-1 text-xs text-muted-foreground">
    <span>Next steps are off for this chat. Turn them back on in the mode menu › Replies.</span>
    {onTurnOn && <button type="button" onClick={() => { setOff(false); onTurnOn() }} data-testid="next-steps-undo-off"
      className="rounded px-1 text-foreground underline-offset-2 hover:underline focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring">Undo</button>}
  </p>
  return <section ref={ref} aria-label="Summary and next steps" data-testid="next-steps-card"
    className="my-3 rounded-xl border border-border bg-background px-3.5 py-3 text-sm shadow-sm">
    <div className="flex items-start gap-2">
      <div className="min-w-0 flex-1 space-y-2">
        {steps.summary && <p className="text-[13px] leading-5 text-muted-foreground" data-testid="next-steps-summary">{steps.summary}</p>}
        <p className="font-medium text-foreground">{steps.question}</p>
      </div>
      <button type="button" onClick={onDismiss} aria-label="Dismiss" title="Dismiss"
        className="shrink-0 rounded p-0.5 text-muted-foreground hover:bg-muted hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring">
        <X className="h-4 w-4" aria-hidden="true" />
      </button>
    </div>
    <div className="mt-2.5 flex flex-wrap gap-2">
      {steps.options.map((option, index) => {
        const first = index === 0 && shortcut
        const mode = nextOptionMode(option, modes)
        const key = first && <kbd aria-hidden="true" className={`rounded border px-1 text-[10px] leading-4 ${mode === 'plan' ? 'border-border' : mode === 'goal' ? 'border-current/30' : 'border-background/30'}`}>{isMac ? '⌘↵' : 'Ctrl↵'}</kbd>
        const focus = 'focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring'
        // A goal or a plan (owner, 28 Nov): the button names the mode and shows the objective under it.
        if (mode) {
          const objective = mode === 'goal' ? option.goal! : option.plan!
          const look = mode === 'goal'
            ? (first ? 'border-success bg-success text-success-foreground hover:bg-success/90' : 'border-success/40 bg-success/10 text-success hover:bg-success/15')
            : 'border-border bg-background text-foreground hover:bg-muted'
          return <button key={`${mode}:${objective}`} type="button" onClick={() => onPick(option)} data-testid={`next-steps-option-${index}`} data-mode={mode}
            title={`${mode === 'goal' ? 'Start as a goal' : 'Plan first'}: ${objective}${first ? ` (${isMac ? '⌘' : 'Ctrl'}+Enter)` : ''}`}
            className={`inline-flex min-w-0 max-w-[22rem] flex-col items-start gap-px rounded-lg border px-3 py-1.5 text-left text-[13px] ${look} ${focus}`}>
            <span className="inline-flex items-center gap-1.5 font-semibold">
              {mode === 'goal' ? <span aria-hidden="true">🎯</span> : <ListChecks className="h-3.5 w-3.5" aria-hidden="true" />}
              {mode === 'goal' ? 'Start as goal' : 'Plan first'}{key}
            </span>
            <span className="max-w-full truncate text-[11.5px] opacity-80">{objective}</span>
          </button>
        }
        return <button key={option.text} type="button" onClick={() => onPick(option)} data-testid={`next-steps-option-${index}`}
          title={first ? `Send "${option.text}" (${isMac ? '⌘' : 'Ctrl'}+Enter)` : `Send "${option.text}"`}
          className={`inline-flex max-w-full items-center gap-1.5 rounded-lg px-3 py-1.5 text-[13px] ${focus} ${first ? 'bg-foreground text-background hover:bg-foreground/90' : 'bg-muted text-foreground hover:bg-muted/70'}`}>
          <span className="truncate">{option.text}</span>
          {key}
        </button>
      })}
      {/* Owner, 26 Nov: a quiet "Turn off" on the answers' row; where it turns back on is in its tooltip. */}
      {onTurnOff && <button type="button" onClick={() => { setOff(true); onTurnOff() }} data-testid="next-steps-turn-off"
        title="Stop these cards in this chat. Turn them back on in the mode menu › Replies."
        className="ml-auto self-end rounded px-1 text-[11px] text-muted-foreground/70 hover:text-foreground hover:underline focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring">Turn off</button>}
    </div>
  </section>
}
