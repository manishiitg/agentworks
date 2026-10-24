// @vitest-environment happy-dom
import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, expect, it, vi } from 'vitest'
vi.hoisted(() => vi.stubGlobal('localStorage', { getItem: () => null, setItem: () => {}, removeItem: () => {} }))
import { useChatStore, type ChatTab } from '../../stores/useChatStore'
import { useGlobalPresetStore } from '../../stores/useGlobalPresetStore'
import { WorkflowChatTabs } from './WorkflowChatTabs'
import { useProductSurfaceStore } from '../../stores/useProductSurfaceStore'
import { StepInspectionTabs } from '../chat/StepInspectionTabs'
import { inspectionKey, openStepInspection, selectStepInspection } from '../../utils/stepInspection'
import type { ExecutionInspectionTarget } from '../../services/api-types'

vi.mock('../../utils/activateTab', () => ({ activateTab: (id: string) => { useChatStore.getState().switchTab(id); return true } }))
vi.mock('../chat/AgentWorksChatTabItem', () => ({ AgentWorksChatTabItem: (props: any) => <button aria-pressed={props.isActive} onClick={() => props.onTabClick(props.tab.tabId)}>{props.displayName}</button> }))
vi.mock('../chat/StepInspectionPanel', () => ({ default: ({ target }: { target: ExecutionInspectionTarget }) => <p>Saved conversation: {target.execution_id}</p> }))
Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
const originalChat = useChatStore.getState()
const originalPresets = useGlobalPresetStore.getState()
const originalSurface = useProductSurfaceStore.getState()
let root: Root
afterEach(() => {
  act(() => root?.unmount()); document.body.innerHTML = ''
  useChatStore.setState(originalChat, true); useGlobalPresetStore.setState(originalPresets, true); useProductSurfaceStore.setState(originalSurface, true)
})
const target: ExecutionInspectionTarget = { session_id: 'parent', execution_id: 'record-prices', workspace_path: 'Workflow/example', step_id: 'prices', name: 'Record Prices', started_at: '2026-10-22T00:00:00Z' }
const makeTab = (tabId: string, sessionId: string, metadata: ChatTab['metadata']) => ({ tabId, sessionId, name: tabId, isStreaming: false, createdAt: 1, config: {}, metadata }) as ChatTab
async function render() {
  useGlobalPresetStore.setState({ activePresetIds: { ...originalPresets.activePresetIds, workflow: 'example' }, workflowPresets: [{ id: 'example', label: 'Example' } as any] })
  useChatStore.setState({ activeTabId: 'chat', tabEvents: {}, chatTabs: {
    chat: makeTab('chat', 'parent', { mode: 'workflow', presetQueryId: 'example', phaseId: 'workflow-builder' }),
    schedule: makeTab('schedule', 'run', { mode: 'workflow', presetQueryId: 'example', isViewOnly: true, isScheduledRun: true }),
    foreign: makeTab('foreign', 'other', { mode: 'workflow', presetQueryId: 'other', isViewOnly: true, stepInspections: { items: [{ ...target, session_id: 'other', name: 'Foreign step' }], activeId: null } }),
  } })
  const host = document.createElement('div'); document.body.append(host); root = createRoot(host)
  function View() {
    const tabId = useChatStore(state => state.activeTabId)
    return <><header><WorkflowChatTabs embedded /></header><main><StepInspectionTabs tabId={tabId} navigation="shared"><input defaultValue="Keep my draft" /></StepInspectionTabs></main></>
  }
  await act(async () => root.render(<View />))
  return host
}
const button = (host: HTMLElement, label: string) => [...host.querySelectorAll<HTMLButtonElement>('button')].find(item => item.getAttribute('aria-label') === label || item.textContent === label)!

it('opens a left-sidebar step in the same strip and returns to the parent draft without removing run tabs', async () => {
  const host = await render()
  const draft = host.querySelector('input')!
  await act(async () => { expect(openStepInspection(target)).toBe(true) })
  await vi.waitFor(() => expect(host.textContent).toContain('Saved conversation: record-prices'))
  const strip = host.querySelector('[aria-label="Chat tabs"]')!
  expect(strip.contains(button(host, 'Inspect Record Prices'))).toBe(true)
  expect(host.querySelector('main [role="tablist"]')).toBeNull()
  expect(button(host, 'Example').getAttribute('aria-pressed')).toBe('false')
  expect(button(host, 'Inspect Record Prices').getAttribute('aria-pressed')).toBe('true')
  expect(host.textContent).not.toContain('Foreign step')
  await act(async () => button(host, 'Example').click())
  expect(host.querySelector('input')).toBe(draft)
  expect(draft.value).toBe('Keep my draft')
  expect(host.querySelector('main')?.textContent).not.toContain('Saved conversation:')
  expect(button(host, 'Inspect Record Prices')).toBeTruthy()
  expect(button(host, 'schedule')).toBeTruthy()
  await act(async () => button(host, 'Inspect Record Prices').click())
  expect(host.textContent).toContain('Saved conversation: record-prices')
  await act(async () => button(host, 'Close inspection Record Prices').click())
  expect(useChatStore.getState().getTab('chat')?.sessionId).toBe('parent')
  expect(useChatStore.getState().getTab('schedule')?.sessionId).toBe('run')
  expect(host.querySelector('input')).toBe(draft)
  expect(button(host, 'Inspect Record Prices')).toBeUndefined()
})

it('keeps step targets scoped to their parent sessions when switching or closing another tab', async () => {
  const host = await render()
  await act(async () => { openStepInspection(target) })
  await act(async () => button(host, 'schedule').click())
  await act(async () => { expect(openStepInspection({ ...target, session_id: 'run', execution_id: 'run-step', name: 'Run step' })).toBe(true) })
  await act(async () => button(host, 'Close inspection Record Prices').click())
  expect(useChatStore.getState().activeTabId).toBe('schedule')
  expect(button(host, 'Inspect Run step').getAttribute('aria-pressed')).toBe('true')
  expect(host.textContent).toContain('Saved conversation: run-step')
  expect(selectStepInspection('schedule', 'old-session', null)).toBe(false)
  expect(selectStepInspection('schedule', 'run', inspectionKey(target))).toBe(false)
  expect(useChatStore.getState().getTab('schedule')?.metadata?.stepInspections?.items).toHaveLength(1)
})


it.each(['agentworks', 'relays'] as const)('names another owner once in the %s tab strip and preserves switching', async surface => {
  const host = await render()
  await act(async () => {
    useProductSurfaceStore.setState({ productSurface: surface })
    const metadata = { mode: 'workflow' as const, phaseId: 'workflow-builder' as const, presetQueryId: 'example' }
    const owner = { agentProfileTabOwnerID: 'other', agentProfileTabOwnerName: 'alex.taylor@example.com', agentProfileTabCanWrite: false }
    useChatStore.setState({ activeTabId: 'mine', chatTabs: {
      mine: { ...makeTab('mine', 'mine-session', { ...metadata, workflowOwnerTabID: 'mine', agentProfileTabOwnerID: 'me', agentProfileTabOwnerName: 'me@example.com', agentProfileTabCanWrite: true }), name: 'My Builder' },
      research: { ...makeTab('research', 'research-session', { ...metadata, ...owner, workflowOwnerTabID: 'research' }), name: 'Research' },
      review: { ...makeTab('review', 'review-session', { ...metadata, ...owner, workflowOwnerTabID: 'review' }), name: 'Review' },
    } })
  })
  expect(host.querySelectorAll('[data-chat-owner-label]')).toHaveLength(1)
  expect(host.querySelector('[data-chat-owner-label]')?.textContent).toBe('Alex T.')
  expect(host.querySelector('[data-chat-owner-group="me"]')).toBeNull()
  await act(async () => button(host, 'Review').click())
  expect(useChatStore.getState().activeTabId).toBe('review')
  expect(host.querySelectorAll('[data-chat-owner-label]')).toHaveLength(1)
})
