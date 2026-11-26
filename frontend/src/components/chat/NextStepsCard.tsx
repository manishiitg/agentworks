import { useCallback, useEffect, useMemo, useRef, useState, useSyncExternalStore, type ReactNode } from 'react'
import { createPortal } from 'react-dom'
import { X } from 'lucide-react'
import { useChatStore } from '../../stores/useChatStore'
import { currentNextStepsEvent, nextStepsOf, type NextSteps } from '../../utils/nextSteps'

// Summary and next steps (owner, 26 Nov, like Claude Desktop): the agent's last reply ended with a short summary, one
// question and two or three answers. Shown after that reply in the chat, scrolling with it like Claude Desktop's (owner,
// 26 Nov: not pinned to the message box), until the person types, sends or dismisses it; never saved. The first answer also goes with Cmd+Enter (ChatInput).

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

export function NextStepsCard({ steps, onPick, onDismiss, onTurnOff }: { steps: NextSteps; onPick: (text: string) => void; onDismiss: () => void; onTurnOff?: () => void }) {
  const ref = useRef<HTMLElement>(null)
  // It appears as the reply ends; bring it into view the way the reply itself was.
  useEffect(() => { ref.current?.scrollIntoView?.({ block: 'nearest', behavior: 'smooth' }) }, [])
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
      {steps.options.map((option, index) => <button key={option} type="button" onClick={() => onPick(option)} data-testid={`next-steps-option-${index}`}
        title={index === 0 ? `Send "${option}" (${isMac ? '⌘' : 'Ctrl'}+Enter)` : `Send "${option}"`}
        className={`inline-flex max-w-full items-center gap-1.5 rounded-lg px-3 py-1.5 text-[13px] focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring ${index === 0 ? 'bg-foreground text-background hover:bg-foreground/90' : 'bg-muted text-foreground hover:bg-muted/70'}`}>
        <span className="truncate">{option}</span>
        {index === 0 && <kbd aria-hidden="true" className="rounded border border-background/30 px-1 text-[10px] leading-4">{isMac ? '⌘↵' : 'Ctrl↵'}</kbd>}
      </button>)}
    </div>
    {/* Owner, 26 Nov: say how to turn it off, and where it turns back on. */}
    {onTurnOff && <p className="mt-2 text-right text-[11px] text-muted-foreground" data-testid="next-steps-turn-off-row">
      <button type="button" onClick={onTurnOff} data-testid="next-steps-turn-off" className="underline-offset-2 hover:text-foreground hover:underline focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring rounded">Turn off for this chat</button>
      <span> · on or off in the mode menu by the model, under Replies</span>
    </p>}
  </section>
}
