import { useState } from 'react'
import { ArrowLeft } from 'lucide-react'
import { CrewExecutablesPanel } from './CrewExecutablesPanel'
import { PlanRunsSwitch, type PlanRunsSection } from './PlanRunsSwitch'

/**
 * The Crew's Workflow page (owner approved the mock 8 Nov): a big two-button switch,
 * Plan (what it does) and Runs (what happened). The Scripts card sits under the plan;
 * a Crew with only scripts shows just the Scripts card.
 */
export function CrewWorkflowPage({ projectId, chatTabId, onBack }: { projectId: string; chatTabId: string; onBack?: () => void }) {
  const [section, setSection] = useState<PlanRunsSection>('plan')
  const [scriptsOnly, setScriptsOnly] = useState(false)
  return <div data-testid="crew-workflow-page" className="flex h-full min-h-0 w-full flex-col bg-background">
    <div className="min-h-0 flex-1 overflow-auto">
      <div className="mx-auto w-full max-w-4xl space-y-4 p-4">
        {!scriptsOnly
          ? <PlanRunsSwitch section={section} onChange={setSection} onBack={onBack} />
          : onBack && <button type="button" aria-label="Back to the project menu" title="Back" onClick={onBack} className="flex items-center gap-1 text-xs text-muted-foreground hover:text-foreground"><ArrowLeft className="h-4 w-4" /> Back</button>}
        <CrewExecutablesPanel key={projectId} projectId={projectId} chatTabId={chatTabId} kind="workflow" section={section} onScriptsOnly={setScriptsOnly} />
      </div>
    </div>
  </div>
}
