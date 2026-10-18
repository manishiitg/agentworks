import { useState } from 'react'
import { useInfiniteQuery } from '@tanstack/react-query'
import { Check, CircleAlert, GitBranch, Loader2 } from 'lucide-react'
import { agentApi } from '../../services/api'
import type { ExecutionInspectionTarget, PollingEvent, WorkspaceStepInspection } from '../../services/api-types'
import { useChatStore } from '../../stores/useChatStore'
import { useAuthStore } from '../../stores/useAuthStore'
import { useSidebarHistoryStore } from '../../stores/useSidebarHistoryStore'
import { enabledProductSurfaces, intersectAllowedProductSurfaces } from '../../products/productSurfaceConfig'
import { captureChatIdentity, isChatIdentityCurrent } from '../../utils/chatIdentity'
import { inspectionKey, isWorkflowInspectionTab, openStepInspection } from '../../utils/stepInspection'
import { sidebarStepInspections, sidebarInspectionReceipts } from '../../utils/sidebarStepInspections'
import type { SidebarChat } from '../../utils/sidebarChats'
const emptyEvents: PollingEvent[] = []

export default function SidebarStepInspections({ row, workspace, scope, visible, selected, onOpen, receipts }: {
  row: SidebarChat
  receipts?: WorkspaceStepInspection[]
  workspace: string
  scope: string
  visible: boolean
  selected: boolean
  onOpen: () => Promise<void>
}) {
  const tab = useChatStore(state => row.tabId ? state.chatTabs[row.tabId] : Object.values(state.chatTabs).find(tab => tab.sessionId === row.id && isWorkflowInspectionTab(tab)))
  const selectedTab = useChatStore(state => state.activeTabId)
  const events = useChatStore(state => state.tabEvents[row.id] ?? emptyEvents)
  const [limit, setLimit] = useState(4)
  const [opening, setOpening] = useState<string | null>(null)
  const enabled = visible && row.surface === 'agentworks' && workspace.startsWith('Workflow/')
  const query = useInfiniteQuery({
    queryKey: ['sidebar-step-receipts-v1', scope, row.id, workspace],
    initialPageParam: '',
    queryFn: ({ pageParam }) => agentApi.getWorkspaceStepInspections(workspace, { sessionId: row.id, cursor: pageParam }),
    getNextPageParam: page => page.next_cursor ?? undefined,
    enabled: enabled && !receipts,
    retry: false,
    staleTime: 10000,
    refetchInterval: enabled && row.group === 'Running' ? 5000 : false,
    refetchIntervalInBackground: false,
  })
  // An access/error response must not leave a stale cached execution visible.
  const fallback = sidebarStepInspections(row.id, workspace, undefined,
    isWorkflowInspectionTab(tab) ? tab?.metadata?.stepInspections?.items : undefined, events)
  const steps = query.isError && !receipts ? [] : [...new Map([
    ...fallback, ...sidebarInspectionReceipts(row.id, workspace, receipts || query.data?.pages.flatMap(page => page.items)),
  ].map(step => [inspectionKey(step.target), step])).values()]
  const open = async (target: ExecutionInspectionTarget) => {
    const identity = captureChatIdentity()
    setOpening(inspectionKey(target))
    try {
      const navigation = onOpen()
      const generation = useSidebarHistoryStore.getState().generation
      await navigation
      if (!isChatIdentityCurrent(identity) || generation !== useSidebarHistoryStore.getState().generation) return
      const user = useAuthStore.getState().user
      if (!intersectAllowedProductSurfaces(enabledProductSurfaces(), user?.allowed_products).includes('agentworks') ||
        (user?.allowed_workflow_ids && (!row.workflowId || !user.allowed_workflow_ids.includes(row.workflowId)))) return
      if (!openStepInspection(target)) throw new Error('Parent chat unavailable')
    } catch {
      if (isChatIdentityCurrent(identity)) useChatStore.getState().addToast('Could not open this step. Open its chat and try Inspect.', 'error')
    } finally { setOpening(null) }
  }
  if (!enabled || (query.isError && !receipts) || (!steps.length && (receipts || !query.hasNextPage))) return null
  return <div aria-label={`Steps in ${row.title}`} className="mb-1 ml-5 border-l border-border/60 pl-1">
    {steps.slice(0, limit).map(({target, status}) => {
      const key = inspectionKey(target)
      const active = selected && tab?.tabId === selectedTab && tab.metadata?.stepInspections?.activeId === key
      const working = status === 'running'
      const label = target.name || target.step_id || 'Step'
      return <button key={key} type="button" title={`Inspect ${label}${status ? ` · ${status}` : ''}`}
        aria-label={`Inspect ${label}`} aria-current={active ? 'page' : undefined}
        onClick={() => void open(target)}
        className={`group mb-0.5 flex min-h-8 w-full items-center gap-2 rounded-md px-2 py-1.5 text-left text-xs focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring ${active ? 'bg-foreground/[0.08] text-foreground' : 'text-muted-foreground hover:bg-muted hover:text-foreground'}`}>
        <GitBranch className="h-3 w-3 shrink-0 opacity-60" aria-hidden="true" />
        <span className="min-w-0 flex-1 truncate">{label}</span>
        {working || opening === key ? <Loader2 aria-label={working ? 'Step running' : 'Opening step'} className="h-3 w-3 shrink-0 animate-spin text-primary" /> : status === 'completed' ? <Check aria-label="Step completed" className="h-3 w-3 shrink-0" /> : status === 'failed' ? <CircleAlert aria-label="Step failed" className="h-3 w-3 shrink-0 text-amber-500" /> : null}
        <span className="text-[10px] opacity-0 group-hover:opacity-100 group-focus-visible:opacity-100">Inspect</span>
      </button>
    })}
    {!receipts && query.hasNextPage && steps.length <= limit && <button type="button" disabled={query.isFetchingNextPage} className="rounded px-2 py-1 text-[11px] text-muted-foreground hover:text-foreground" onClick={() => void query.fetchNextPage()}>Earlier steps</button>}
    {steps.length > limit && <button type="button" className="rounded px-2 py-1 text-[11px] text-muted-foreground hover:text-foreground" onClick={() => setLimit(value => value + 8)}>Show {steps.length - limit} more steps</button>}
  </div>
}
