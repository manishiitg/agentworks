import { useState } from 'react'
import { CrewExecutablesPanel } from './CrewExecutablesPanel'

type Section = 'plan' | 'runs'

/**
 * The Crew's Workflow page (owner approved the mock 8 Nov): a big two-button switch,
 * Plan (what it does) and Runs (what happened). The Scripts card sits under the plan;
 * a Crew with only scripts shows just the Scripts card.
 */
export function CrewWorkflowPage({ projectId, chatTabId }: { projectId: string; chatTabId: string }) {
  const [section, setSection] = useState<Section>('plan')
  const [scriptsOnly, setScriptsOnly] = useState(false)
  return <div data-testid="crew-workflow-page" className="flex h-full min-h-0 w-full flex-col bg-background">
    <div className="min-h-0 flex-1 overflow-auto">
      <div className="mx-auto w-full max-w-4xl space-y-4 p-4">
        {!scriptsOnly && <nav aria-label="Workflow" className="flex gap-1 rounded-2xl border border-border p-1">
          {([['plan', 'Plan', 'what it does'], ['runs', 'Runs', 'what happened']] as const).map(([value, label, hint]) =>
            <button key={value} type="button" aria-current={section === value ? 'page' : undefined} onClick={() => setSection(value)}
              className={`flex-1 rounded-xl border px-2 py-2 text-center ${section === value ? 'border-border bg-card text-foreground' : 'border-transparent text-muted-foreground hover:text-foreground'}`}>
              <span className="block text-sm font-semibold">{label}</span>
              <span className="block text-xs">{hint}</span>
            </button>)}
        </nav>}
        <CrewExecutablesPanel key={projectId} projectId={projectId} chatTabId={chatTabId} kind="workflow" section={section} onScriptsOnly={setScriptsOnly} />
      </div>
    </div>
  </div>
}
