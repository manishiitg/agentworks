import { useInfiniteQuery } from '@tanstack/react-query'
import { useAuthStore } from '../../stores/useAuthStore'
import { useChatStore } from '../../stores/useChatStore'
import { useSidebarHistoryStore } from '../../stores/useSidebarHistoryStore'
import { agentApi } from '../../services/api'
import type { WorkspaceStepInspection, ActiveSessionInfo } from '../../services/api-types'
import { enabledProductSurfaces, intersectAllowedProductSurfaces } from '../../products/productSurfaceConfig'
import { captureChatIdentity, isChatIdentityCurrent } from '../../utils/chatIdentity'
import { openWorkflowInspectionParent } from '../../utils/workflowSessionRestore'
import { workflowTriggerLabel } from '../../utils/workflowSessionKinds'
import { sidebarInspectionReceipts } from '../../utils/sidebarStepInspections'
import SidebarStepInspections from './SidebarStepInspections'

function source(parent: WorkspaceStepInspection['parent']): string {
  const platform = parent.bot_platform?.toLowerCase()
  if (platform === 'slack') return 'Slack'
  if (platform === 'whatsapp') return 'WhatsApp'
  if (parent.triggered_by?.toLowerCase() === 'mcp' || platform === 'mcp') return 'MCP'
  const trigger = workflowTriggerLabel({ sessionId: parent.session_id, triggeredBy: parent.triggered_by })
  return trigger === 'Scheduled' ? 'Schedule' : trigger === 'Webhook' ? 'Webhook' : 'Other runs'
}

/** Receipts whose parent is absent from the visible chat list still belong to
 * their Goal. Discovery never opens a chat or hydrates a transcript by itself. */
export default function SidebarWorkspaceInspections({ workspace, workflowId, scope, visible, selected, listedSessions }: {
  workspace: string; workflowId: string; scope: string; visible: boolean; selected: boolean; listedSessions: string[]
}) {
  const user = useAuthStore(state => state.user)
  const allowed = intersectAllowedProductSurfaces(enabledProductSurfaces(), user?.allowed_products).includes('agentworks') &&
    (!user?.allowed_workflow_ids || user.allowed_workflow_ids.includes(workflowId))
  const excluded = [...new Set(listedSessions)].sort().slice(0, 50)
  const query = useInfiniteQuery({
    queryKey: ['workspace-step-inspections', scope, workspace, workflowId, excluded],
    initialPageParam: 0,
    queryFn: ({ pageParam }) => agentApi.getWorkspaceStepInspections(workspace, { excludeSessionIds: excluded, offset: pageParam }),
    getNextPageParam: page => page.next_offset ?? undefined,
    enabled: allowed && visible && selected && workspace.startsWith('Workflow/'),
    retry: false,
    staleTime: 10000,
    refetchInterval: visible && selected && allowed ? 30000 : false,
    refetchIntervalInBackground: false,
  })
  if (!allowed || !visible || query.isError) return null
  const parents = new Map<string, WorkspaceStepInspection[]>()
  for (const page of query.data?.pages || []) for (const receipt of page.items) {
    const id = receipt.parent.session_id
    if (excluded.includes(id) || !sidebarInspectionReceipts(id, workspace, [receipt]).length) continue
    parents.set(id, [...parents.get(id) || [], receipt])
  }
  if (!parents.size && !query.hasNextPage) return null
  return <div aria-label="Steps from other runs" className="mt-1">
    {[...parents].map(([id, receipts]) => {
      const origin = source(receipts[0].parent)
      const date = receipts[0].inspection.started_at
      const title = `${origin}${date ? ` · ${new Date(date).toLocaleString(undefined, { month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit' })}` : ''}`
      return <div key={id}>
        <div className="px-2 py-1 text-[10px] text-muted-foreground">{title}</div>
        <SidebarStepInspections
          row={{ id, title, workspace, workflowId, surface: 'agentworks', group: 'Recent', waiting: false, time: 0 }}
          workspace={workspace} scope={scope} visible selected={selected} receipts={receipts}
          onOpen={async () => {
            const identity = captureChatIdentity()
            const current = useAuthStore.getState().user
            if (!isChatIdentityCurrent(identity) ||
              !intersectAllowedProductSurfaces(enabledProductSurfaces(), current?.allowed_products).includes('agentworks') ||
              (current?.allowed_workflow_ids && !current.allowed_workflow_ids.includes(workflowId))) return
            useSidebarHistoryStore.setState(state => ({ request: null, generation: state.generation + 1 }))
            await openWorkflowInspectionParent({ ...receipts[0].parent, agent_mode: 'workflow', preset_query_id: workflowId } as ActiveSessionInfo, title)
          }}
        />
      </div>
    })}
    {query.hasNextPage && <button type="button" disabled={query.isFetchingNextPage}
      className="px-2 py-1 text-[11px] text-muted-foreground hover:text-foreground"
      onClick={() => void query.fetchNextPage()}>Earlier steps</button>}
  </div>
}
