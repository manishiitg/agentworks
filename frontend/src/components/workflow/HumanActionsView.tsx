import { Hand } from 'lucide-react'
import { ReportHumanInputPanel } from './ReportHumanInputPanel'
import { WorkspaceViewHeader } from './WorkspaceViewHeader'

export default function HumanActionsView({ workspacePath }: { workspacePath: string | null }) {
  return (
    <div className="flex h-full min-h-0 flex-col bg-background">
      <WorkspaceViewHeader
        icon={Hand}
        title="Decisions"
        subtitle="Decisions waiting for your direction, with previous answers and outcomes"
      />
      <div className="min-h-0 flex-1 overflow-y-auto p-3 sm:p-4">
        {workspacePath && <ReportHumanInputPanel workspacePath={workspacePath} contentMode="all" showEmptyState />}
      </div>
    </div>
  )
}
