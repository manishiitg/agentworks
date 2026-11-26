import { useMemo, useState } from 'react'
import { X } from 'lucide-react'
import { useChatStore } from '../../stores/useChatStore'
import { currentNextStepsEvent, nextStepsOf, type NextSteps } from '../../utils/nextSteps'

// Summary and next steps (owner, 26 Nov, like Claude Desktop): the agent's last reply ended with a short summary, one
// question and two or three answers. Shown above the message box until the person types, sends or dismisses it;
// never saved. The first answer also goes with Cmd+Enter (ChatInput).

export function useNextSteps(session: string | null, available: boolean) {
  const event = useChatStore(state => session ? currentNextStepsEvent(state.tabEvents[session]) : undefined)
  const steps = useMemo(() => nextStepsOf(event), [event])
  const [dismissed, setDismissed] = useState<string>()
  const shown = available && event && steps && event.id !== dismissed ? steps : undefined
  return { steps: shown, dismiss: () => setDismissed(event?.id) }
}

const isMac = typeof navigator !== 'undefined' && /Mac|iPhone|iPad/.test(navigator.platform || navigator.userAgent || '')

export function NextStepsCard({ steps, onPick, onDismiss }: { steps: NextSteps; onPick: (text: string) => void; onDismiss: () => void }) {
  return <section aria-label="Summary and next steps" data-testid="next-steps-card"
    className="mb-2 rounded-xl border border-border bg-background px-3.5 py-3 text-sm shadow-sm">
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
  </section>
}
