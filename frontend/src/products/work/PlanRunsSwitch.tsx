import { PageTabs } from '../../components/automation/AutomationKit'

export type PlanRunsSection = 'plan' | 'runs'

/** The switch at the top of a workflow or script page: what it does, and what happened; the back arrow sits beside it. */
export function PlanRunsSwitch({ section, onChange, onBack }: {
  section: PlanRunsSection; onChange: (next: PlanRunsSection) => void; onBack?: () => void
}) {
  return <PageTabs label="Workflow" value={section} onChange={onChange} onBack={onBack}
    tabs={[{ id: 'plan', label: 'Plan', hint: 'what it does' }, { id: 'runs', label: 'Runs', hint: 'what happened' }]} />
}
