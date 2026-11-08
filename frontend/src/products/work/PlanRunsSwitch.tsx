import { PageTabs } from '../../components/automation/AutomationKit'

export type PlanRunsSection = 'plan' | 'runs'

/** The switch at the top of a workflow or script page: what it does, and what happened; the back arrow sits beside it. */
export function PlanRunsSwitch({ section, onChange, onBack, planLabel = 'Plan', planHint = 'what it does' }: {
  section: PlanRunsSection; onChange: (next: PlanRunsSection) => void; onBack?: () => void; planLabel?: string; planHint?: string
}) {
  return <PageTabs label="Workflow" value={section} onChange={onChange} onBack={onBack}
    tabs={[{ id: 'plan', label: planLabel, hint: planHint }, { id: 'runs', label: 'Runs', hint: 'what happened' }]} />
}
