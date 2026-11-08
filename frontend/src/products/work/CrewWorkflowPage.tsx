import { useState } from 'react'
import { ChevronRight, FileCode2, Route } from 'lucide-react'
import { WorkspaceViewHeader } from '../../components/workflow/WorkspaceViewHeader'
import { CrewExecutablesPanel } from './CrewExecutablesPanel'

/**
 * The Crew's Workflow page (owner approved the mock 8 Nov): the plan and its runs,
 * with Scripts as a card. A Crew with only scripts shows just the Scripts card.
 */
export function CrewWorkflowPage({ projectId, chatTabId }: { projectId: string; chatTabId: string }) {
  const [found, setFound] = useState<{ workflows: number; scripts: number } | null>(null)
  const [showScripts, setShowScripts] = useState(false)
  const scriptsOnly = found !== null && found.workflows === 0 && found.scripts > 0
  const scripts = showScripts || scriptsOnly
  return <div data-testid="crew-workflow-page" className="flex h-full min-h-0 w-full flex-col bg-background">
    <WorkspaceViewHeader icon={Route} title="Workflow" />
    {scripts && !scriptsOnly && <button type="button" onClick={() => setShowScripts(false)} className="shrink-0 border-b px-3 py-2 text-left text-sm text-muted-foreground hover:text-foreground">‹ Workflow</button>}
    <div className="min-h-0 flex-1 overflow-auto">
      {scripts
        ? <CrewExecutablesPanel key={`${projectId}:scripts`} projectId={projectId} chatTabId={chatTabId} kind="script" />
        : <CrewExecutablesPanel key={`${projectId}:workflows`} projectId={projectId} chatTabId={chatTabId} kind="workflow" onLoaded={setFound} />}
    </div>
    {!scripts && <button type="button" onClick={() => setShowScripts(true)} className="flex shrink-0 items-center gap-2 border-t px-3 py-3 text-left text-sm hover:bg-muted">
      <FileCode2 className="h-4 w-4" /><span className="flex-1">Scripts{found ? ` · ${found.scripts}` : ''}</span><ChevronRight className="h-4 w-4" />
    </button>}
  </div>
}
