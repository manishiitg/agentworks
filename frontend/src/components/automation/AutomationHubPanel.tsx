import { cloneElement, isValidElement, lazy, Suspense, useEffect, useMemo, useState, type ReactElement, type ReactNode } from 'react'
import { Zap } from 'lucide-react'
import api from '../../services/api'
import type { ProductTriggerScope } from '../../api/productWebhooks'
import { useWorkflowStore } from '../../stores/useWorkflowStore'
import { TriggerDeliveryHistoryPanel } from './TriggerDeliveryHistoryPanel'
import type { WorkflowScope } from '../scheduler/scheduleRuns/helpers'
import { ScheduleStatusPills, type ScheduleStatusSnapshot } from '../scheduler/scheduleRuns/ScheduleStatusPills'
import { sendWorkspacePaneMessageToChat } from '../../utils/workspacePaneChat'
import { WorkspaceViewHeader } from '../workflow/WorkspaceViewHeader'
import { AgentInboxPanel, useAgentInboxEnabled } from '../integrations/AgentInboxPanel'
import { WorkspaceViewIconButton } from '../workflow/WorkspaceViewIconButton'

const WorkflowScheduleRunsPanel = lazy(() => import('../scheduler/WorkflowScheduleRunsPanel'))
const ProductAPITriggersView = lazy(() => import('../workflow/ProductAPITriggersView'))
const WorkflowAPITriggersView = lazy(() => import('../workflow/WorkflowAPITriggersView'))
const CrewFunctionsView = lazy(() => import('./CrewFunctionsView'))
const WorkflowFunctionsView = lazy(() => import('../workflow/WorkflowFunctionsView'))
const ConditionTriggersView = lazy(() => import('./ConditionTriggersView'))

export type AutomationHubSection = 'chats' | 'schedules' | 'triggers' | 'functions' | 'bots' | 'conditions' | 'email'

type AutomationHubPanelProps = {
  entityType: 'workflow' | 'product'
  relayMode?: boolean
  relayWorkflowID?: string
  workspacePath: string
  workflowScope?: WorkflowScope
  productTriggerScope?: ProductTriggerScope
  chatContent?: ReactNode
  botContent?: ReactNode
  canManage?: boolean
  scopeNoun?: 'automation' | 'project'
  initialSection?: AutomationHubSection
  /** Chat routing override for section actions (Crew panes route to the project chat). */
  onAskAI?: (message: string) => void | Promise<void>
}

export function AutomationHubPanel({
  entityType,
  relayMode = false,
  relayWorkflowID,
  workspacePath,
  workflowScope,
  productTriggerScope,
  chatContent,
  botContent,
  canManage,
  scopeNoun = entityType === 'product' ? 'project' : 'automation',
  initialSection = 'schedules',
  onAskAI,
}: AutomationHubPanelProps) {
  const [children, setChildren] = useState<Array<{ id: string; workspace_path: string; manifest: { label: string } }>>([])
  const [childrenError, setChildrenError] = useState('')
  const [childFunctionCounts, setChildFunctionCounts] = useState<Record<string, number>>({})
  const [childWebhookCounts, setChildWebhookCounts] = useState<Record<string, { active: number; paused: number }>>({})
  const crewID = entityType === 'product' && productTriggerScope?.profileId === 'work' && canManage !== false ? productTriggerScope.projectId : undefined
  useEffect(() => {
    const controller = new AbortController()
    setChildren([]); setChildrenError(''); setChildFunctionCounts({}); setChildWebhookCounts({})
    if (crewID) void api.get('/api/crew-executables', { params: { crew_id: crewID }, signal: controller.signal }).then(response => {
      if (!controller.signal.aborted) setChildren(response.data.executables)
    }).catch(() => { if (!controller.signal.aborted) setChildrenError('Could not load workflow automations. Reopen Automations to try again.') })
    return () => controller.abort()
  }, [crewID])
  const incomingEnabled = useAgentInboxEnabled()
  const isWorkflow = entityType === 'workflow'
  const [schedulesRefreshToken, setSchedulesRefreshToken] = useState(0)
  const [schedulesStatus, setSchedulesStatus] = useState<ScheduleStatusSnapshot | null>(null)
  const [emailRefreshToken, setEmailRefreshToken] = useState(0)
  const [triggersRefreshToken, setTriggersRefreshToken] = useState(0)
  const [triggersCounts, setTriggersCounts] = useState<{ active: number; paused: number } | null>(null)
  const [chatsRefreshToken, setChatsRefreshToken] = useState(0)
  const [functionsRefreshToken, setFunctionsRefreshToken] = useState(0)
  const [functionsCounts, setFunctionsCounts] = useState<{ functions: number; running: number } | null>(null)
  const workspaceViewTarget = useWorkflowStore(state => state.workspaceViewTarget)
  const availableSections = useMemo(() => new Set<AutomationHubSection>([
    ...(incomingEnabled && !isWorkflow && (productTriggerScope?.profileId === 'work' || productTriggerScope?.profileId === 'code') ? ['email' as const] : []),
    ...(chatContent ? ['chats' as const] : []),
    ...(!relayMode ? ['schedules' as const] : []),
    ...((isWorkflow || productTriggerScope?.profileId === 'work' || productTriggerScope?.profileId === 'work') ? ['conditions' as const] : []),
    ...(entityType === 'workflow' || productTriggerScope ? ['triggers' as const] : []),
    // Functions (PLAT-357): a Crew's declared functions, or a workflow's
    // function triggers plus its assistant ask.
    ...(!relayMode && ((entityType === 'product' && productTriggerScope) || entityType === 'workflow') ? ['functions' as const] : []),
    ...(!relayMode && botContent ? ['bots' as const] : []),
  ]), [botContent, chatContent, entityType, productTriggerScope, relayMode, incomingEnabled, isWorkflow])
  const fallbackSection = availableSections.has(initialSection) ? initialSection : relayMode ? 'triggers' : 'schedules'
  const [section, setSection] = useState<AutomationHubSection>(fallbackSection)

  useEffect(() => {
    setSection(fallbackSection)
  }, [fallbackSection])

  useEffect(() => {
    if (workspaceViewTarget?.view !== 'workshop') return
    const target = workspaceViewTarget?.target
    const normalized = target === 'webhooks' ? 'triggers' : target
    if (normalized && availableSections.has(normalized as AutomationHubSection)) {
      setSection(normalized as AutomationHubSection)
      if (normalized === 'email') setEmailRefreshToken(token => token + 1)
    }
  }, [availableSections, workspaceViewTarget])

  // Per-tab refresh, computed from the section. The bots tab has no
  // refreshable list, so it has no header action.
  const crewPage = entityType === 'product' && productTriggerScope?.profileId === 'work'
  const refreshAll = () => { setSchedulesRefreshToken(token => token + 1); setTriggersRefreshToken(token => token + 1); setEmailRefreshToken(token => token + 1) }
  const refreshAction = crewPage
    ? { label: 'Refresh', run: refreshAll, spinning: schedulesStatus?.isLoading }
    : section === 'email'
    ? { label: 'Refresh inbox', run: () => setEmailRefreshToken(token => token + 1), spinning: false }
    : section === 'conditions'
    ? { label: 'Refresh watchers', run: () => setTriggersRefreshToken(token => token + 1), spinning: false }
    : section === 'schedules'
    ? { label: 'Refresh schedules', run: () => setSchedulesRefreshToken(token => token + 1), spinning: schedulesStatus?.isLoading }
    : section === 'triggers'
      ? relayMode
        ? { label: 'Refresh triggers', run: () => setFunctionsRefreshToken(token => token + 1), spinning: false }
        : { label: 'Refresh webhooks', run: () => setTriggersRefreshToken(token => token + 1), spinning: false }
      : section === 'functions'
        ? { label: 'Refresh functions', run: () => setFunctionsRefreshToken(token => token + 1), spinning: false }
      : section === 'chats'
        ? { label: 'Refresh chats', run: () => setChatsRefreshToken(token => token + 1), spinning: false }
        : undefined
  const headerActions = refreshAction ? <WorkspaceViewIconButton label={refreshAction.label} onClick={refreshAction.run} spinning={refreshAction.spinning} /> : undefined

  const ask = onAskAI ?? (message => sendWorkspacePaneMessageToChat({ message, workspacePath }).then(() => undefined))
  const schedulesView = <WorkflowScheduleRunsPanel
    embedded
    active
    entityType={entityType}
    workflowScope={workflowScope}
    canManage={canManage}
    scopeNoun={scopeNoun}
    showAutomationTabs={false}
    hideHeader
    refreshToken={schedulesRefreshToken}
    onStatus={setSchedulesStatus}
    onClose={() => {}}
  />
  const watchersView = <ConditionTriggersView scope={productTriggerScope ?? { profileId: relayMode ? 'relays' : 'agentworks', projectId: workflowScope?.workflowId ?? workspacePath }} refreshToken={triggersRefreshToken} onAsk={ask} canManage={canManage} />
  const productWebhooksView = (wrap: string) => entityType === 'product' && productTriggerScope ? <div className={wrap}><ProductAPITriggersView
    embedded
    hideEmpty={Object.values(childWebhookCounts).some(count => count.active + count.paused > 0)}
    scope={productTriggerScope}
    workspacePath={workspacePath}
    onAsk={onAskAI}
    deliveryHistory={<TriggerDeliveryHistoryPanel workspacePath={workspacePath} entityType="product" productTriggerScope={productTriggerScope} />}
    hideHeader
    refreshToken={triggersRefreshToken}
    onCounts={setTriggersCounts}
  />
    {childrenError && <p role="alert" className="p-4 text-sm text-destructive">{childrenError}</p>}
    {children.map(child => <WorkflowAPITriggersView key={child.id} workspacePath={child.workspace_path} embedded hideHeader onCounts={count => setChildWebhookCounts(previous => previous[child.id]?.active === count.active && previous[child.id]?.paused === count.paused ? previous : { ...previous, [child.id]: count })} refreshToken={triggersRefreshToken} onAsk={message => onAskAI?.(`${message}\nWorkflow: ${child.manifest.label}\nWorkspace: ${child.workspace_path}`)} />)}
  </div> : null
  const inboxView = (wrap: string) => <div className={wrap}><AgentInboxPanel workspacePath={workspacePath} refreshToken={emailRefreshToken} /></div>

  return (
    <div
      data-testid="automation-hub-panel"
      className="flex h-full min-h-0 min-w-0 w-full max-w-none flex-1 flex-col bg-background"
    >
      <WorkspaceViewHeader
        icon={Zap}
        title="Automations"
        actions={headerActions}
        context={
          section === 'schedules' && schedulesStatus ? <ScheduleStatusPills status={schedulesStatus} />
          : section === 'triggers' && relayMode && functionsCounts ? (
            <span className="text-xs text-muted-foreground">{functionsCounts.functions} functions</span>
          ) : section === 'triggers' && triggersCounts ? (
            <span className="text-xs text-muted-foreground">{triggersCounts.active + Object.values(childWebhookCounts).reduce((sum, count) => sum + count.active, 0)} active · {triggersCounts.paused + Object.values(childWebhookCounts).reduce((sum, count) => sum + count.paused, 0)} paused</span>
          ) : section === 'functions' && functionsCounts ? (
            <span className="text-xs text-muted-foreground">{functionsCounts.functions + Object.values(childFunctionCounts).reduce((sum, count) => sum + count, 0)} functions · {functionsCounts.running} running</span>
          ) : undefined
        }

      />

      <div className="min-h-0 flex-1 overflow-hidden">
        <Suspense fallback={<div className="p-4 text-sm text-muted-foreground">Loading…</div>}>
        {crewPage ? <div className="h-full space-y-4 overflow-auto p-4">
          <AutomationCard title="Schedules" onAsk={() => ask('Help me set up a schedule for this Crew.')}>{schedulesView}</AutomationCard>
          <AutomationCard title="Webhooks" hint="Another app starts this by calling a web address." onAsk={() => ask('Help me set up a webhook for this Crew.')}>{productWebhooksView('')}</AutomationCard>
          <AutomationCard title="Watchers" hint="Keeps checking something, and acts when it finds a match." onAsk={() => ask('Help me set up a watcher for this Crew.')}>{watchersView}</AutomationCard>
          {availableSections.has('email') && <AutomationCard title="Inbox" hint="Messages sent to this Crew’s email address; each starts a chat." onAsk={() => ask('Help me with this Crew’s inbox.')}>{inboxView('')}</AutomationCard>}
        </div> : <>
        {section === 'chats' && chatContent && (
          <div data-testid="automation-hub-chats" className="flex h-full min-h-0 flex-col">
            {isValidElement(chatContent)
              ? cloneElement(chatContent as ReactElement<{ refreshToken?: number }>, { refreshToken: chatsRefreshToken })
              : chatContent}
          </div>
        )}
        {!relayMode && section === 'schedules' && schedulesView}
        {section === 'triggers' && entityType === 'workflow' && !relayMode && <WorkflowAPITriggersView
          workspacePath={workspacePath}
          onAsk={onAskAI}
          deliveryHistory={<TriggerDeliveryHistoryPanel workspacePath={workspacePath} entityType="workflow" />}
          hideHeader
          refreshToken={triggersRefreshToken}
          onCounts={setTriggersCounts}
        />}
        {section === 'email' && inboxView('h-full overflow-y-auto p-4')}
        {section === 'conditions' && watchersView}
        {section === 'triggers' && entityType === 'workflow' && relayMode && <WorkflowFunctionsView
          workspacePath={workspacePath}
          relayMode
          relayWorkflowID={relayWorkflowID}
          refreshToken={functionsRefreshToken}
          onCounts={setFunctionsCounts}
          onAsk={onAskAI ?? (message => sendWorkspacePaneMessageToChat({ message, workspacePath }).then(() => undefined))}
        />}
        {section === 'triggers' && productWebhooksView('h-full overflow-auto')}
        {section === 'functions' && entityType === 'workflow' && <WorkflowFunctionsView
          workspacePath={workspacePath}
          refreshToken={functionsRefreshToken}
          onCounts={setFunctionsCounts}
          onAsk={onAskAI ?? (message => sendWorkspacePaneMessageToChat({ message, workspacePath }).then(() => undefined))}
        />}
        {section === 'functions' && entityType === 'product' && productTriggerScope && <div className="h-full overflow-auto"><CrewFunctionsView
          embedded
          hideEmpty={Object.values(childFunctionCounts).some(count => count > 0)}
          scope={productTriggerScope}
          refreshToken={functionsRefreshToken}
          onCounts={setFunctionsCounts}
          onEdit={onAskAI}
        />
          {childrenError && <p role="alert" className="p-4 text-sm text-destructive">{childrenError}</p>}
          {children.map(child => <WorkflowFunctionsView key={child.id} workspacePath={child.workspace_path} embedded onCounts={count => setChildFunctionCounts(previous => previous[child.id] === count.functions ? previous : { ...previous, [child.id]: count.functions })} refreshToken={functionsRefreshToken} onAsk={message => onAskAI?.(`${message}\nWorkflow: ${child.manifest.label}\nWorkspace: ${child.workspace_path}`)} />)}
        </div>}
        {section === 'bots' && botContent}
        </>}
        </Suspense>
      </div>
    </div>
  )
}

function AutomationCard({ title, hint, onAsk, children }: { title: string; hint?: string; onAsk: () => void | Promise<void>; children: ReactNode }) {
  return <section aria-label={title} className="rounded-lg border bg-card">
    <div className="flex items-start gap-2 border-b px-4 py-3">
      <div className="min-w-0 flex-1">
        <h3 className="text-sm font-semibold">{title}</h3>
        {hint && <p className="text-xs text-muted-foreground">{hint}</p>}
      </div>
      <button type="button" onClick={() => void onAsk()} className="shrink-0 rounded-md px-2 py-1 text-xs text-primary hover:bg-muted">Ask</button>
    </div>
    <div>{children}</div>
  </section>
}
