import { ListChecks } from 'lucide-react'

// The Plan switch beside the model (#3699, approved mockup): while on, each message plans first in the
// program's own plan mode. Shift+Tab in the message box toggles it.
export function PlanModeChip({ on, onToggle }: { on: boolean; onToggle: () => void }) {
  return (
    <button type="button" onClick={onToggle} aria-pressed={on} data-testid="plan-mode-chip"
      title="Plan first: the agent reads and proposes a plan, and changes nothing until you approve (Shift+Tab)"
      className={`inline-flex h-6 items-center gap-1 rounded-full border px-2 text-[11px] focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring ${on
        ? 'border-primary bg-primary/10 text-primary'
        : 'border-border bg-muted/40 text-muted-foreground hover:bg-muted hover:text-foreground'}`}>
      <ListChecks className="h-3 w-3" aria-hidden="true" />
      <span>Plan</span>
      <kbd className="font-mono text-[10px] opacity-70">⇧⇥</kbd>
    </button>
  )
}

// Codex and Cursor answer a plan as a reply: one step to start the work, which turns Plan off.
export function PlanStartBar({ onStart }: { onStart: () => void }) {
  return (
    <div className="mx-3 my-1 flex items-center gap-2 text-xs" data-testid="plan-start-bar">
      <span className="text-muted-foreground">The plan is ready.</span>
      <button type="button" onClick={onStart}
        className="rounded-md bg-primary px-2.5 py-1 font-medium text-primary-foreground hover:bg-primary/90 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring">
        Start the work
      </button>
    </div>
  )
}
