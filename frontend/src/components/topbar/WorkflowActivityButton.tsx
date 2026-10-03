import { useCallback, useEffect, useRef, useState } from 'react'
import { Inbox } from 'lucide-react'
import { agentApi } from '../../services/api'
import { workflowHasActivity, workflowHasRecentActivity } from '../../utils/workflowActivity'
import { Tooltip, TooltipContent, TooltipTrigger } from '../ui/tooltip'
import { whenWorkflowChatSettled } from '../../utils/whenWorkflowChatSettled'
import { useLiveRefetch } from '../../hooks/useLiveRefetch'

const WORKFLOW_ACTIVITY_REFRESH_MS = 30_000

interface WorkflowActivityButtonProps {
  workspacePath: string
  onOpen: () => void
}

/** A compact entry point to the selected automation's Activity view. */
export function WorkflowActivityButton({ workspacePath, onOpen }: WorkflowActivityButtonProps) {
  const [activityState, setActivityState] = useState({ available: false, recent: false })
  const refreshSequence = useRef(0)

  const refresh = useCallback(async () => {
    const request = ++refreshSequence.current
    try {
      const [notifications, humanInputs] = await Promise.all([
        agentApi.getOrgDashboardNotifications([workspacePath], 1),
        agentApi.listNeedsYouDecisionsAggregate([workspacePath]).catch(() => ({ success: false, inputs: [] })),
      ])
      if (request === refreshSequence.current) {
        const inputs = humanInputs.inputs || []
        setActivityState({
          available: workflowHasActivity(workspacePath, notifications, inputs),
          recent: workflowHasRecentActivity(workspacePath, notifications, inputs),
        })
      }
    } catch {
      // A failed probe should not expose an activity affordance that may open
      // an empty/error state. The next poll will retry automatically.
      if (request === refreshSequence.current) setActivityState({ available: false, recent: false })
    }
  }, [workspacePath])

  useEffect(() => {
    let disposed = false
    const requestRefresh = () => {
      if (disposed || document.hidden) return
      void refresh()
    }

    setActivityState({ available: false, recent: false })
    void whenWorkflowChatSettled().then(requestRefresh)
    return () => {
      disposed = true
      refreshSequence.current += 1
    }
  }, [refresh])
  // Refetch on live notices; polling only if the feed is down.
  useLiveRefetch(() => { void refresh() }, { kinds: ['notifications', 'human_inputs'], workflow: workspacePath, fallbackMs: WORKFLOW_ACTIVITY_REFRESH_MS })

  if (!activityState.available) return null

  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <button
          type="button"
          onClick={onOpen}
          aria-label="Activity for this automation"
          className={`flex h-6 w-7 items-center justify-center rounded transition-colors hover:bg-background/70 hover:text-foreground ${activityState.recent ? 'text-sky-500/70 dark:text-sky-400/70' : 'text-muted-foreground'}`}
        >
          <Inbox className="h-3.5 w-3.5" />
        </button>
      </TooltipTrigger>
      <TooltipContent side="bottom"><p>{activityState.recent ? 'Activity · Updated in the last 24 hours' : 'Activity for this automation'}</p></TooltipContent>
    </Tooltip>
  )
}
