import { useMemo } from 'react'
import { Share2 } from 'lucide-react'
import WorkflowSharePopup from '../../components/workflow/WorkflowSharePopup'
import { WorkspaceViewHeader } from '../../components/workflow/WorkspaceViewHeader'
import { codeShareSource } from './codeShareSource'

/**
 * Setup → Share for a Code: the same sharing UI as a workflow's access list,
 * over the Code's share list. Co-owners change it; everyone else in the Code
 * sees who has access.
 */
export function CodeSharePanel({ projectId, workspacePath }: { projectId: string; workspacePath: string }) {
  const source = useMemo(() => codeShareSource(projectId), [projectId])
  return (
    <div className="flex h-full min-h-0 flex-col bg-background">
      <WorkspaceViewHeader icon={Share2} title="Share" helpTopic="Share" subtitle="Who can open this Code and what they can do." />
      <div className="min-h-0 flex-1 overflow-y-auto p-4">
        <WorkflowSharePopup workspacePath={workspacePath} source={source} />
      </div>
    </div>
  )
}
