import { cloneElement, isValidElement, lazy, Suspense, useCallback, useEffect, useMemo, useState, type ReactElement, type ReactNode } from 'react'
import { Bot, Braces, CalendarClock, MessageSquareText, Webhook, Zap } from 'lucide-react'
import type { ProductTriggerScope } from '../../api/productWebhooks'
import { useWorkflowStore } from '../../stores/useWorkflowStore'
import { TriggerDeliveryHistoryPanel } from './TriggerDeliveryHistoryPanel'
import type { WorkflowScope } from '../scheduler/scheduleRuns/helpers'
import { ScheduleStatusPills, type ScheduleStatusSnapshot } from '../scheduler/scheduleRuns/ScheduleStatusPills'
import { AskAIButton } from '../workflow/AskAIButton'
import { sendWorkspacePaneMessageToChat } from '../../utils/workspacePaneChat'
import { getWorkspaceAskAIMessage } from '../workflow/workspaceAskAI'
import { WorkspaceViewHeader } from '../workflow/WorkspaceViewHeader'
import { WorkspaceViewActions } from '../workflow/WorkspaceViewActions'
import { WorkspaceViewIconButton } from '../workflow/WorkspaceViewIconButton'

const WorkflowScheduleRunsPanel = lazy(() => import('../scheduler/WorkflowScheduleRunsPanel'))
const ProductAPITriggersView = lazy(() => import('../workflow/ProductAPITriggersView'))
const WorkflowAPITriggersView = lazy(() => import('../workflow/WorkflowAPITriggersView'))
const CrewFunctionsView = lazy(() => import('./CrewFunctionsView'))
const WorkflowFunctionsView = lazy(() => import('../workflow/WorkflowFunctionsView'))

export type AutomationHubSection = 'chats' | 'schedules' | 'triggers' | 'functions' | 'bots'

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
  /**
   * Per-tab Ask AI messages. Workflow hubs default to the workspace messages;
   * product (Crew) hubs must pass explicit messages: a workspacePath-routed
   * message would land in the wrong chat.
   */
  askAIMessages?: Partial<Record<AutomationHubSection, string>>
  /** Ask AI routing override (Crew panes route to the project chat). */
  onAskAI?: (message: string) => void | Promise<void>
}

const SECTION_DEFS = [
  { id: 'schedules', label: 'Schedules', icon: CalendarClock },
  { id: 'triggers', label: 'Webhooks', icon: Webhook },
  { id: 'functions', label: 'Functions', icon: Braces },
  { id: 'bots', label: 'Bots', icon: Bot },
  { id: 'chats', label: 'Chats', icon: MessageSquareText },
] as const

const WORKFLOW_SECTION_MESSAGES: Record<AutomationHubSection, string> = {
  schedules: getWorkspaceAskAIMessage('schedules'),
  triggers: getWorkspaceAskAIMessage('webhooks'),
  functions: getWorkspaceAskAIMessage('webhooks'),
  chats: getWorkspaceAskAIMessage('workshop'),
  bots: getWorkspaceAskAIMessage('workshop'),
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
  askAIMessages,
  onAskAI,
}: AutomationHubPanelProps) {
  // Workflow hubs default every Ask AI message to the workflow Builder chat.
  // Product (Crew) hubs must pass explicit messages: a workspacePath-routed
  // message would land in the wrong chat.
  const isWorkflow = entityType === 'workflow'
  const [schedulesRefreshToken, setSchedulesRefreshToken] = useState(0)
  const [schedulesStatus, setSchedulesStatus] = useState<ScheduleStatusSnapshot | null>(null)
  const [triggersRefreshToken, setTriggersRefreshToken] = useState(0)
  const [triggersCounts, setTriggersCounts] = useState<{ active: number; paused: number } | null>(null)
  const [chatsRefreshToken, setChatsRefreshToken] = useState(0)
  const [functionsRefreshToken, setFunctionsRefreshToken] = useState(0)
  const [functionsCounts, setFunctionsCounts] = useState<{ functions: number; running: number } | null>(null)
  const workspaceViewTarget = useWorkflowStore(state => state.workspaceViewTarget)
  const availableSections = useMemo(() => new Set<AutomationHubSection>([
    ...(chatContent ? ['chats' as const] : []),
    ...(!relayMode ? ['schedules' as const] : []),
    ...(entityType === 'workflow' || productTriggerScope ? ['triggers' as const] : []),
    // Functions (PLAT-357): a Crew's declared functions, or a workflow's
    // function triggers plus its assistant ask.
    ...(!relayMode && ((entityType === 'product' && productTriggerScope) || entityType === 'workflow') ? ['functions' as const] : []),
    ...(!relayMode && botContent ? ['bots' as const] : []),
  ]), [botContent, chatContent, entityType, productTriggerScope, relayMode])
  const fallbackSection = availableSections.has(initialSection) ? initialSection : relayMode ? 'triggers' : 'schedules'
  const [section, setSection] = useState<AutomationHubSection>(fallbackSection)
  const selectSection = useCallback((next: AutomationHubSection) => {
    setSection(next)
    useWorkflowStore.getState().openWorkspaceView('workshop', next)
  }, [])

  useEffect(() => {
    if (availableSections.has(initialSection)) setSection(initialSection)
  }, [availableSections, initialSection])

  useEffect(() => {
    if (workspaceViewTarget?.view !== 'workshop') return
    const target = workspaceViewTarget?.target
    const normalized = target === 'webhooks' ? 'triggers' : target
    if (normalized && availableSections.has(normalized as AutomationHubSection)) {
      setSection(normalized as AutomationHubSection)
    }
  }, [availableSections, workspaceViewTarget])

  const askMessage = askAIMessages?.[section] ?? (isWorkflow ? WORKFLOW_SECTION_MESSAGES[section] : undefined)
  // Per-tab refresh, computed from the section so the header renders the
  // standard pair (Ask AI left, refresh right). The bots tab has no
  // refreshable list, so it keeps Ask AI alone.
  const refreshAction = section === 'schedules'
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
  const askAction = askMessage ? <AskAIButton workspacePath={workspacePath} message={askMessage} onAsk={onAskAI} iconOnly /> : undefined
  const refreshButton = refreshAction ? <WorkspaceViewIconButton label={refreshAction.label} onClick={refreshAction.run} spinning={refreshAction.spinning} /> : undefined
  const headerActions = askMessage && refreshAction ? (
    <WorkspaceViewActions workspacePath={workspacePath} message={askMessage} onAsk={onAskAI} onRefresh={refreshAction.run} refreshLabel={refreshAction.label} refreshing={refreshAction.spinning ?? false} />
  ) : (askAction || refreshButton) ? (
    <>{askAction}{refreshButton}</>
  ) : undefined

  return (
    <div
      data-testid="automation-hub-panel"
      className="flex h-full min-h-0 min-w-0 w-full max-w-none flex-1 flex-col bg-background"
    >
      <WorkspaceViewHeader
        icon={Zap}
        title={relayMode ? 'Relay' : 'Automation'}
        helpTopic={`Automation · ${SECTION_DEFS.find(item => item.id === section)?.label ?? 'Chats'}`}
        subtitle="Chat history and the channels that can start work."
        actions={headerActions}
        context={
          section === 'schedules' && schedulesStatus ? <ScheduleStatusPills status={schedulesStatus} />
          : section === 'triggers' && relayMode && functionsCounts ? (
            <span className="text-xs text-muted-foreground">{functionsCounts.functions} functions</span>
          ) : section === 'triggers' && triggersCounts ? (
            <span className="text-xs text-muted-foreground">{triggersCounts.active} active · {triggersCounts.paused} paused</span>
          ) : section === 'functions' && functionsCounts ? (
            <span className="text-xs text-muted-foreground">{functionsCounts.functions} functions · {functionsCounts.running} running</span>
          ) : undefined
        }
        tabs={{
          value: section,
          onChange: (value: string) => selectSection(value as AutomationHubSection),
          options: SECTION_DEFS.filter(item => availableSections.has(item.id)).map(({ id, label, icon }) => ({ value: id, label: relayMode && id === 'triggers' ? 'Triggers' : label, icon })),
          ariaLabel: 'Automation center',
        }}
      />

      <div className="min-h-0 flex-1 overflow-hidden">
        <Suspense fallback={<div className="p-4 text-sm text-muted-foreground">Loading…</div>}>
        {section === 'chats' && chatContent && (
          <div data-testid="automation-hub-chats" className="flex h-full min-h-0 flex-col">
            {isValidElement(chatContent)
              ? cloneElement(chatContent as ReactElement<{ refreshToken?: number }>, { refreshToken: chatsRefreshToken })
              : chatContent}
          </div>
        )}
        {section === 'schedules' && <WorkflowScheduleRunsPanel
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
        />}
        {section === 'triggers' && entityType === 'workflow' && !relayMode && <WorkflowAPITriggersView
          workspacePath={workspacePath}
          deliveryHistory={<TriggerDeliveryHistoryPanel workspacePath={workspacePath} entityType="workflow" />}
          hideHeader
          refreshToken={triggersRefreshToken}
          onCounts={setTriggersCounts}
        />}
        {section === 'triggers' && entityType === 'workflow' && relayMode && <WorkflowFunctionsView
          workspacePath={workspacePath}
          relayMode
          relayWorkflowID={relayWorkflowID}
          refreshToken={functionsRefreshToken}
          onCounts={setFunctionsCounts}
          onAsk={onAskAI ?? (message => sendWorkspacePaneMessageToChat({ message, workspacePath }).then(() => undefined))}
        />}
        {section === 'triggers' && entityType === 'product' && productTriggerScope && <ProductAPITriggersView
          scope={productTriggerScope}
          deliveryHistory={<TriggerDeliveryHistoryPanel workspacePath={workspacePath} entityType="product" productTriggerScope={productTriggerScope} />}
          hideHeader
          refreshToken={triggersRefreshToken}
          onCounts={setTriggersCounts}
        />}
        {section === 'functions' && entityType === 'workflow' && <WorkflowFunctionsView
          workspacePath={workspacePath}
          refreshToken={functionsRefreshToken}
          onCounts={setFunctionsCounts}
          onAsk={onAskAI ?? (message => sendWorkspacePaneMessageToChat({ message, workspacePath }).then(() => undefined))}
        />}
        {section === 'functions' && entityType === 'product' && productTriggerScope && <CrewFunctionsView
          scope={productTriggerScope}
          refreshToken={functionsRefreshToken}
          onCounts={setFunctionsCounts}
          onEdit={onAskAI}
        />}
        {section === 'bots' && botContent}
        </Suspense>
      </div>
    </div>
  )
}
