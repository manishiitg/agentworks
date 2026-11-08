export type PlanRunsSection = 'plan' | 'runs'

/** The big two-button switch at the top of a workflow or script page: what it does, and what happened. */
export function PlanRunsSwitch({ section, onChange, planLabel = 'Plan', planHint = 'what it does' }: {
  section: PlanRunsSection; onChange: (next: PlanRunsSection) => void; planLabel?: string; planHint?: string
}) {
  return <nav aria-label="Workflow" className="flex gap-1 rounded-2xl border border-border p-1">
    {([['plan', planLabel, planHint], ['runs', 'Runs', 'what happened']] as const).map(([value, label, hint]) =>
      <button key={value} type="button" aria-current={section === value ? 'page' : undefined} onClick={() => onChange(value)}
        className={`flex-1 rounded-xl border px-2 py-2 text-center ${section === value ? 'border-border bg-card text-foreground' : 'border-transparent text-muted-foreground hover:text-foreground'}`}>
        <span className="block text-sm font-semibold">{label}</span>
        <span className="block text-xs">{hint}</span>
      </button>)}
  </nav>
}
