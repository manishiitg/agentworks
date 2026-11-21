import { PageTabs } from '../../components/automation/AutomationKit'

export type StepsRunsSection = 'steps' | 'runs' | 'files' | 'starts'

/**
 * The switch at the top of a workflow page: its tasks (what it does), its runs (what happened), its files and what
 * starts it (its schedules and webhooks, owner-approved mockup 19 Nov).
 * A workflow can hold several tasks, written in its Python script, that are independent or connected.
 * The section id stays `steps`; only the label says Tasks.
 */
export function StepsRunsSwitch({ section, onChange, onBack }: {
  section: StepsRunsSection; onChange: (next: StepsRunsSection) => void; onBack?: () => void
}) {
  return <PageTabs label="Workflow" value={section} onChange={onChange} onBack={onBack}
    tabs={[{ id: 'steps', label: 'Tasks', hint: 'what it does' }, { id: 'runs', label: 'Runs', hint: 'what happened' }, { id: 'files', label: 'Files', hint: 'what it is made of' }, { id: 'starts', label: 'Starts', hint: 'what sets it off' }]} />
}
