// @vitest-environment happy-dom
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { expect, it, vi } from 'vitest'

const storeState = { workspaceViewTarget: null as null | { view: string; target: string; token: number } }
const openWorkspaceView = vi.fn()

vi.mock('../../stores/useWorkflowStore', () => ({
  useWorkflowStore: Object.assign(
    (selector: (state: unknown) => unknown) => selector(storeState),
    { getState: () => ({ openWorkspaceView }) },
  ),
}))
const { schedulesPanelProps } = vi.hoisted(() => ({ schedulesPanelProps: { current: null as null | Record<string, unknown> } }))
vi.mock('../scheduler/WorkflowScheduleRunsPanel', () => ({
  default: (props: Record<string, unknown>) => {
    schedulesPanelProps.current = props
    return <div data-testid="schedules" data-hide-header={String(Boolean(props.hideHeader))} data-refresh-token={String(props.refreshToken ?? 0)}>Schedule content</div>
  },
}))
const { triggersPanelProps } = vi.hoisted(() => ({ triggersPanelProps: { current: null as null | Record<string, unknown> } }))
vi.mock('../workflow/ProductAPITriggersView', () => ({ default: (props: Record<string, unknown>) => {
  triggersPanelProps.current = props
  return <div data-testid="triggers" data-hide-header={String(Boolean(props.hideHeader))} data-refresh-token={String(props.refreshToken ?? 0)}>Trigger content</div>
} }))
vi.mock('../workflow/WorkflowAPITriggersView', () => ({ default: (props: Record<string, unknown>) => {
  triggersPanelProps.current = props
  return <div data-testid="workflow-triggers" data-hide-header={String(Boolean(props.hideHeader))} data-refresh-token={String(props.refreshToken ?? 0)}>Workflow trigger content</div>
} }))
vi.mock('../workflow/WorkflowFunctionsView', () => ({ default: (props: Record<string, unknown>) => {
  triggersPanelProps.current = props
  return <div data-testid="relay-triggers">Relay API triggers</div>
} }))
vi.mock('./ConditionTriggersView', () => ({ default: ({ scope }: { scope: { profileId: string; projectId: string } }) => <div data-testid="conditions" data-source={scope.profileId} data-project={scope.projectId}>Watchers</div> }))
vi.mock('./CrewFunctionsView', () => ({ default: () => <div data-testid="functions">Function content</div> }))
vi.mock('./TriggerDeliveryHistoryPanel', () => ({ TriggerDeliveryHistoryPanel: () => <div data-testid="delivery-history" /> }))
const { askAIProps } = vi.hoisted(() => ({ askAIProps: { current: null as null | Record<string, unknown> } }))
vi.mock('../workflow/AskAIButton', () => ({
  AskAIButton: (props: Record<string, unknown>) => {
    askAIProps.current = props
    return <button type="button" data-testid="ask-ai" data-message={String(props.message)}>Ask AI</button>
  },
}))

const { inboxCapability } = vi.hoisted(()=>({inboxCapability:{enabled:true}}))
vi.mock('../integrations/AgentInboxPanel', () => ({ useAgentInboxEnabled:()=>inboxCapability.enabled, agentInboxAskMessage: 'Configure inbox Watcher', AgentInboxPanel: (props: { workspacePath: string; onAsk?: (message:string) => void }) => <button data-testid="incoming-email" onClick={() => props.onAsk?.('email help')}>{props.workspacePath}</button> }))
import { AutomationHubPanel } from './AutomationHubPanel'

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })

// Navigation is supplied by the shared side list. Pin inbox routing rather than the retired tab layout.
it.each(['work','code'] as const)('opens the shared Inbox for %s and keeps Ask in its product', async profile => {
 const host=document.createElement('div');const root=createRoot(host);const onAsk=vi.fn()
 inboxCapability.enabled=true
 storeState.workspaceViewTarget={view:'workshop',target:'email',token:1}
 try {
  await act(async()=>root.render(<AutomationHubPanel entityType={profile==='work'||profile==='code'?'product':'workflow'} relayMode={profile==='relays'} workspacePath="Workflow/example" productTriggerScope={profile==='work'||profile==='code'?{profileId:profile,projectId:'example'}:undefined} onAskAI={onAsk}/>))
  const inbox=host.querySelector<HTMLButtonElement>('[data-testid="incoming-email"]')
  expect(inbox).not.toBeNull()
  await act(async()=>inbox!.click())
  expect(onAsk).toHaveBeenCalledWith('email help')
  expect(host.querySelector('[role="tablist"]')).toBeNull()
 }finally{storeState.workspaceViewTarget=null;await act(async()=>root.unmount())}
})
it('does not open an Inbox without the backend capability',async()=>{
 const host=document.createElement('div');const root=createRoot(host)
 inboxCapability.enabled=false;storeState.workspaceViewTarget={view:'workshop',target:'email',token:1}
 try{await act(async()=>root.render(<AutomationHubPanel entityType="product" workspacePath="Crew/example" productTriggerScope={{profileId:'work',projectId:'example'}}/>));expect(host.querySelector('[data-testid="incoming-email"]')).toBeNull()}
 finally{inboxCapability.enabled=true;storeState.workspaceViewTarget=null;await act(async()=>root.unmount())}
})
