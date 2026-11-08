import { CrewExecutablesPanel } from './CrewExecutablesPanel'

/** The Crew's Workflow page (owner approved the mock 8 Nov): Steps (what it does) and Runs (what happened). */
export function CrewWorkflowPage({ projectId, chatTabId, onBack }: { projectId: string; chatTabId: string; onBack?: () => void }) {
  return <div data-testid="crew-workflow-page" className="flex h-full min-h-0 w-full flex-col bg-background">
    <div className="min-h-0 flex-1 overflow-auto">
      <div className="mx-auto w-full max-w-4xl space-y-4 p-4">
        <CrewExecutablesPanel key={projectId} projectId={projectId} chatTabId={chatTabId} onBack={onBack} />
      </div>
    </div>
  </div>
}
