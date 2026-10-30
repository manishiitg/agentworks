import { cloneElement, isValidElement, lazy, Suspense, useCallback, useEffect, useMemo, useState, type ReactElement, type ReactNode } from 'react'
import { Bot, Braces, CalendarClock, Mail, MessageSquareText, Webhook, Zap } from 'lucide-react'
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

const SECTION_DEFS = [
  { id: 'schedules', label: 'Schedules', icon: CalendarClock },
  { id: 'triggers', label: 'Webhooks', icon: Webhook },
  { id: 'email', label: 'Inbox', icon: Mail },
  { id: 'conditions', label: 'Watchers', icon: Zap },
  { id: 'functions', label: 'Functions', icon: Braces },
  { id: 'bots', label: 'Bots', icon: Bot },
  { id: 'chats', label: 'Chats', icon: MessageSquareText },
] as const

// The tab a person last chose is remembered per kind of hub (workflow, Flow,
// or a product such as Code), so coming back to Automation reopens it instead
// of Schedules. Browser storage can be blocked, so every access is guarded.
const SECTION_STORAGE_PREFIX = 'automationHub.section.'

function sectionStorageKey(entityType: string, relayMode: boolean, profileId?: string): string {
  return `${SECTION_STORAGE_PREFIX}${relayMode ? 'relay' : entityType}.${profileId ?? ''}`
}

function readRememberedSection(key: string): AutomationHubSection | null {
  try {
    const value = localStorage.getItem(key)
    return SECTION_DEFS.some(item => item.id === value) ? (value as AutomationHubSection) : null
  } catch {
    return null
  }
}

function rememberSection(key: string, section: AutomationHubSection) {
  try {
    localStorage.setItem(key, section)
  } catch {
    // Not remembered; the tab still works for this visit.
  }
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
  const storageKey = sectionStorageKey(entityType, relayMode, productTriggerScope?.profileId)
  const [preferredSection, setPreferredSection] = useState<AutomationHubSection>(() => readRememberedSection(storageKey) ?? initialSection)
  const fallbackSection = availableSections.has(preferredSection) ? preferredSection : relayMode ? 'triggers' : 'schedules'
  const [section, setSection] = useState<AutomationHubSection>(fallbackSection)
  const selectSection = useCallback((next: AutomationHubSection) => {
    setSection(next)
    setPreferredSection(next)
    rememberSection(storageKey, next)
    useWorkflowStore.getState().openWorkspaceView('workshop', next)
  }, [storageKey])

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
  const refreshAction = section === 'email'
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
            <span className="text-xs text-muted-foreground">{triggersCounts.active} active · {triggersCounts.paused} paused</span>
          ) : section === 'functions' && functionsCounts ? (
            <span className="text-xs text-muted-foreground">{functionsCounts.functions} functions · {functionsCounts.running} running</span>
          ) : undefined
        }
        /* Each automation is its own item in the side list, so this page has no tabs (owner, 2026-10-20). */
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
        {!relayMode && section === 'schedules' && <WorkflowScheduleRunsPanel
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
          onAsk={onAskAI}
          deliveryHistory={<TriggerDeliveryHistoryPanel workspacePath={workspacePath} entityType="workflow" />}
          hideHeader
          refreshToken={triggersRefreshToken}
          onCounts={setTriggersCounts}
        />}
        {section === 'email' && <div className="h-full overflow-y-auto p-4"><AgentInboxPanel workspacePath={workspacePath} refreshToken={emailRefreshToken} /></div>}
        {section === 'conditions' && <ConditionTriggersView scope={productTriggerScope ?? { profileId: relayMode ? 'relays' : 'agentworks', projectId: workflowScope?.workflowId ?? workspacePath }} refreshToken={triggersRefreshToken} onAsk={onAskAI ?? (message => sendWorkspacePaneMessageToChat({ message, workspacePath }).then(() => undefined))} canManage={canManage} />}
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
          workspacePath={workspacePath}
          onAsk={onAskAI}
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
