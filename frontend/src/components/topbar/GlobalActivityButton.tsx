import { ProductNavigationLabel, useProductNavigationSidebar } from '../workspace/ProductTopBar'
import { useCallback, useEffect, useRef, useState } from 'react'
import { Inbox } from 'lucide-react'
import { agentApi } from '../../services/api'
import { workflowHasRecentActivity } from '../../utils/workflowActivity'
import { Tooltip, TooltipContent, TooltipTrigger } from '../ui/tooltip'
import { useLiveRefetch } from '../../hooks/useLiveRefetch'

const ACTIVITY_REFRESH_MS = 30_000

interface GlobalActivityButtonProps {
  workspacePaths: string[]
  active: boolean
  onOpen: () => void
}

export function GlobalActivityButton({ workspacePaths, active, onOpen }: GlobalActivityButtonProps) {
  const sidebar = useProductNavigationSidebar()
  const [hasRecentActivity, setHasRecentActivity] = useState(false)
  const refreshSequence = useRef(0)
  const pathKey = workspacePaths.join('\u0000')

  const refresh = useCallback(async () => {
    const request = ++refreshSequence.current
    if (workspacePaths.length === 0) {
      setHasRecentActivity(false)
      return
    }
    try {
      const [notifications, humanInputs] = await Promise.all([
        agentApi.getOrgDashboardNotifications(workspacePaths, 1),
        agentApi.listNeedsYouDecisionsAggregate(workspacePaths).catch(() => ({ success: false, inputs: [] })),
      ])
      if (request !== refreshSequence.current) return
      const inputs = humanInputs.inputs || []
      setHasRecentActivity(workspacePaths.some(path => workflowHasRecentActivity(path, notifications, inputs)))
    } catch {
      if (request === refreshSequence.current) setHasRecentActivity(false)
    }
  // `pathKey` provides stable value-based identity when callers rebuild the array.
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [pathKey])

  useEffect(() => {
    if (!document.hidden) void refresh()
    return () => { refreshSequence.current += 1 }
  }, [refresh])
  // Refetch on live notices; ACTIVITY_REFRESH_MS polling only if the feed is down.
  useLiveRefetch(() => { void refresh() }, { kinds: ['notifications', 'human_inputs'], fallbackMs: ACTIVITY_REFRESH_MS })

  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <button
          type="button"
          data-product-navigation-action
          onClick={onOpen}
          data-tour="global-activity"
          aria-label="Activity"
          aria-pressed={active}
          className={`rounded-md p-1.5 transition-colors ${active
            ? 'bg-primary/10 text-primary'
            : hasRecentActivity
              ? 'text-sky-500/70 hover:bg-sky-500/5 dark:text-sky-400/70'
              : 'text-muted-foreground hover:bg-muted hover:text-foreground'}`}
        >
          <Inbox className="h-4 w-4 shrink-0" />
          <ProductNavigationLabel>Activity</ProductNavigationLabel>
        </button>
      </TooltipTrigger>
      <TooltipContent side={sidebar ? 'right' : 'bottom'}>{hasRecentActivity ? 'Activity · Updates in the last 24 hours' : 'Activity · Updates'}</TooltipContent>
    </Tooltip>
  )
}
