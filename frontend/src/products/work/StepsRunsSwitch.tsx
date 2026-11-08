import { PageTabs } from '../../components/automation/AutomationKit'

export type StepsRunsSection = 'steps' | 'runs'

/** The switch at the top of a workflow page: what it does, and what happened; the back arrow sits beside it. */
export function StepsRunsSwitch({ section, onChange, onBack }: {
  section: StepsRunsSection; onChange: (next: StepsRunsSection) => void; onBack?: () => void
}) {
  return <PageTabs label="Workflow" value={section} onChange={onChange} onBack={onBack}
    tabs={[{ id: 'steps', label: 'Steps', hint: 'what it does' }, { id: 'runs', label: 'Runs', hint: 'what happened' }]} />
}
