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
import { PageCard, PageTabs } from './AutomationKit'

const WorkflowScheduleRunsPanel = lazy(() => import('../scheduler/WorkflowScheduleRunsPanel'))
const ProductAPITriggersView = lazy(() => import('../workflow/ProductAPITriggersView'))
const WorkflowAPITriggersView = lazy(() => import('../workflow/WorkflowAPITriggersView'))
const CrewFunctionsView = lazy(() => import('./CrewFunctionsView'))
const WorkflowFunctionsView = lazy(() => import('../workflow/WorkflowFunctionsView'))
const ConditionTriggersView = lazy(() => import('./ConditionTriggersView'))

export type AutomationHubSection = 'chats' | 'schedules' | 'triggers' | 'functions' | 'bots' | 'conditions' | 'email'

type AutomationHubPanelProps = {
  entityType: 'workflow' | 'product'
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
  /** A Crew page draws its own back arrow beside its tabs (the pane's title row is hidden). */
  onBack?: () => void
}

export function AutomationHubPanel({
  entityType,
  workspacePath,
  workflowScope,
  productTriggerScope,
  chatContent,
  botContent,
  canManage,
  scopeNoun = entityType === 'product' ? 'project' : 'automation',
  initialSection = 'schedules',
  onAskAI,
  onBack,
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
    ...(['schedules' as const]),
    ...((isWorkflow || productTriggerScope?.profileId === 'work' || productTriggerScope?.profileId === 'work') ? ['conditions' as const] : []),
    ...(entityType === 'workflow' || productTriggerScope ? ['triggers' as const] : []),
    // Functions (PLAT-357): a Crew's declared functions, or a workflow's
    // function triggers plus its assistant ask.
    ...(((entityType === 'product' && productTriggerScope) || entityType === 'workflow') ? ['functions' as const] : []),
    ...(botContent ? ['bots' as const] : []),
  ]), [botContent, chatContent, entityType, productTriggerScope, incomingEnabled, isWorkflow])
  const fallbackSection = availableSections.has(initialSection) ? initialSection : 'schedules'
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
  const refreshAction = section === 'email'
    ? { label: 'Refresh inbox', run: () => setEmailRefreshToken(token => token + 1), spinning: false }
    : section === 'conditions'
    ? { label: 'Refresh watchers', run: () => setTriggersRefreshToken(token => token + 1), spinning: false }
    : section === 'schedules'
    ? { label: 'Refresh schedules', run: () => setSchedulesRefreshToken(token => token + 1), spinning: schedulesStatus?.isLoading }
    : section === 'triggers'
      ? { label: 'Refresh webhooks', run: () => setTriggersRefreshToken(token => token + 1), spinning: false }
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
  const watchersView = <ConditionTriggersView scope={productTriggerScope ?? { profileId: 'agentworks', projectId: workflowScope?.workflowId ?? workspacePath }} refreshToken={triggersRefreshToken} onAsk={ask} canManage={canManage} embedded={crewPage} />
  const productWebhooksView = (wrap: string) => entityType === 'product' && productTriggerScope ? <div className={wrap}><ProductAPITriggersView
    embedded
    pageRows={crewPage}
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
    {children.map(child => <WorkflowAPITriggersView key={child.id} workspacePath={child.workspace_path} embedded pageRows={crewPage} title={child.manifest.label} hideHeader onCounts={count => setChildWebhookCounts(previous => previous[child.id]?.active === count.active && previous[child.id]?.paused === count.paused ? previous : { ...previous, [child.id]: count })} refreshToken={triggersRefreshToken} onAsk={message => onAskAI?.(`${message}\nWorkflow: ${child.manifest.label}\nWorkspace: ${child.workspace_path}`)} />)}
  </div> : null
  const inboxView = (wrap: string) => <div className={wrap}><AgentInboxPanel workspacePath={workspacePath} refreshToken={emailRefreshToken} embedded={crewPage} /></div>

  const askMessage = section === 'triggers' ? 'Help me set up a webhook for this Crew.' : section === 'conditions' ? 'Help me set up a watcher for this Crew.' : section === 'email' ? 'Help me with this Crew’s inbox.' : 'Help me set up a schedule for this Crew.'
  const cardTitle = ({ schedules: ['Schedules', undefined], triggers: ['Webhooks', 'Another app starts this by calling a web address.'], conditions: ['Watchers', 'Keeps checking something, and acts when it finds a match.'], email: ['Inbox', 'Only for a Crew that has an email address or a bot.'] } as Record<string, [string, string | undefined]>)[section]
  // On a Crew page each tab's content sits in one card with its own Ask (owner-approved mock, 8 Nov).
  const inCard = (content: ReactNode) => crewPage && cardTitle ? <div className="h-full overflow-y-auto p-4"><PageCard title={cardTitle[0]} hint={cardTitle[1]} onAsk={canManage === false ? undefined : () => ask(askMessage)} onRefresh={refreshAction?.run} refreshLabel={refreshAction?.label} refreshing={refreshAction?.spinning} action={section === 'schedules' && schedulesStatus ? <ScheduleStatusPills status={schedulesStatus} /> : undefined}>{content}</PageCard></div> : content

  return (
    <div
      data-testid="automation-hub-panel"
      className="flex h-full min-h-0 min-w-0 w-full max-w-none flex-1 flex-col bg-background"
    >
      {!crewPage && <WorkspaceViewHeader
        icon={Zap}
        title="Automations"
        actions={headerActions}
        context={
          section === 'schedules' && schedulesStatus ? <ScheduleStatusPills status={schedulesStatus} />
          : section === 'triggers' && triggersCounts ? (
            <span className="text-xs text-muted-foreground">{triggersCounts.active + Object.values(childWebhookCounts).reduce((sum, count) => sum + count.active, 0)} active · {triggersCounts.paused + Object.values(childWebhookCounts).reduce((sum, count) => sum + count.paused, 0)} paused</span>
          ) : section === 'functions' && functionsCounts ? (
            <span className="text-xs text-muted-foreground">{functionsCounts.functions + Object.values(childFunctionCounts).reduce((sum, count) => sum + count, 0)} functions · {functionsCounts.running} running</span>
          ) : undefined
        }

      />}

      {crewPage && <div className="shrink-0 px-3 pt-3"><PageTabs label="Automations" value={section} onChange={setSection} onBack={onBack}
        tabs={([['schedules', 'Schedules', 'when it runs'], ['triggers', 'Webhooks', 'when an app calls'], ['conditions', 'Watchers', 'when something changes'], ['email', 'Inbox', 'mail it gets']] as const)
          .filter(([id]) => availableSections.has(id)).map(([id, label, hint]) => ({ id, label, hint }))} /></div>}
      <div className="min-h-0 flex-1 overflow-hidden">
        <Suspense fallback={<div className="p-4 text-sm text-muted-foreground">Loading…</div>}>
        <>
        {section === 'chats' && chatContent && (
          <div data-testid="automation-hub-chats" className="flex h-full min-h-0 flex-col">
            {isValidElement(chatContent)
              ? cloneElement(chatContent as ReactElement<{ refreshToken?: number }>, { refreshToken: chatsRefreshToken })
              : chatContent}
          </div>
        )}
        {section === 'schedules' && inCard(schedulesView)}
        {section === 'triggers' && entityType === 'workflow' && <WorkflowAPITriggersView
          workspacePath={workspacePath}
          onAsk={onAskAI}
          deliveryHistory={<TriggerDeliveryHistoryPanel workspacePath={workspacePath} entityType="workflow" />}
          hideHeader
          refreshToken={triggersRefreshToken}
          onCounts={setTriggersCounts}
        />}
        {section === 'email' && inCard(inboxView(crewPage ? '' : 'h-full overflow-y-auto p-4'))}
        {section === 'conditions' && inCard(watchersView)}

        {section === 'triggers' && inCard(productWebhooksView(crewPage ? '' : 'h-full overflow-auto'))}
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
        </>
        </Suspense>
      </div>
    </div>
  )
}
