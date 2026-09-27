import { Hand } from 'lucide-react'
import { useEffect, useState } from 'react'
import { agentApi } from '../../services/api'
import type { PulseImpactLedger } from '../../services/api-types'
import { ReportHumanInputPanel } from './ReportHumanInputPanel'
import { WorkspaceViewHeader } from './WorkspaceViewHeader'
import { WORKFLOW_DECISIONS_REFRESH_EVENT, WORKFLOW_LOG_REFRESH_EVENT } from './workflowEvents'

export default function HumanActionsView({ workspacePath }: { workspacePath: string | null }) {
  const [impact, setImpact] = useState<PulseImpactLedger | undefined>()
  useEffect(() => {
    if (!workspacePath) return
    let active = true
    const refreshImpact = () => {
      void agentApi.getPulseImpact(workspacePath).then(response => {
        if (active && response.success) setImpact(response.impact)
      }).catch(() => { /* Decision cards remain usable if impact history is unavailable. */ })
    }
    setImpact(undefined)
    refreshImpact()
    window.addEventListener(WORKFLOW_DECISIONS_REFRESH_EVENT, refreshImpact)
    window.addEventListener(WORKFLOW_LOG_REFRESH_EVENT, refreshImpact)
    return () => {
      active = false
      window.removeEventListener(WORKFLOW_DECISIONS_REFRESH_EVENT, refreshImpact)
      window.removeEventListener(WORKFLOW_LOG_REFRESH_EVENT, refreshImpact)
    }
  }, [workspacePath])
  return (
    <div className="flex h-full min-h-0 flex-col bg-background">
      <WorkspaceViewHeader
        icon={Hand}
        title="Human actions"
        subtitle="Decisions waiting for your direction, with previous answers and outcomes"
      />
      <div className="min-h-0 flex-1 overflow-y-auto p-3 sm:p-4">
        {workspacePath && <ReportHumanInputPanel workspacePath={workspacePath} contentMode="all" providedImpact={impact} showEmptyState />}
      </div>
    </div>
  )
}
