import { beforeEach, expect, it, vi } from 'vitest'
import type { ChatTab } from '../stores/useChatStore'
const state = vi.hoisted(() => ({ tabs: {} as Record<string, ChatTab>, identity: 0, getLead: vi.fn(), send: vi.fn(), open: vi.fn(), activate: vi.fn(() => true), showChat: vi.fn() }))
vi.mock('../services/api', () => ({ agentApi: { getGoalLead: state.getLead } }))
vi.mock('../stores/useChatStore', () => ({ useChatStore: {
 getState: () => ({ chatTabs: state.tabs }),
 setState: (apply: (state: { chatTabs: Record<string, ChatTab> }) => { chatTabs: Record<string, ChatTab> }) => { state.tabs = apply({ chatTabs: state.tabs }).chatTabs },
} }))
vi.mock('../stores/useWorkflowStore', () => ({ useWorkflowStore: { getState: () => ({ setShowChatArea: state.showChat, setFocusedPane: vi.fn() }) } }))
vi.mock('./activateTab', () => ({ activateTab: state.activate }))
vi.mock('./globalProductNavigation', () => ({ openGlobalActivitySession: state.open }))
vi.mock('./workspacePaneChat', () => ({ sendWorkspacePaneMessageToChat: state.send }))
vi.mock('./chatIdentity', () => ({ captureChatIdentity: () => state.identity, assertChatIdentityCurrent: (identity: number) => { if (identity !== state.identity) throw new Error('Chat account changed') } }))
import { askWorkflowPulse, openWorkflowPulseChatTab } from './pulseChatTab'
beforeEach(() => {
 vi.clearAllMocks(); state.identity = 0; state.tabs = {}
 state.getLead.mockResolvedValue({ success: true, conversation: { has_goal: true, session_id: 'schedule-goallead--goal-g1', busy: false } })
 state.open.mockImplementation(async (session: { session_id: string }) => {
  state.tabs.pulse = { tabId: 'pulse', sessionId: session.session_id, name: 'Scheduled run', metadata: { mode: 'workflow', presetQueryId: 'goal', isViewOnly: true, isScheduledRun: true, isExecutionRun: true }, config: { inputText: 'Keep my draft' } } as ChatTab
 })
})
it('opens the exact Autopilot conversation as interactive and sends contextual questions there while keeping its draft', async () => {
 expect(await openWorkflowPulseChatTab('Workflow/example')).toBe('pulse')
 expect(state.open.mock.calls[0][0].status).toBe('completed')
 expect(state.tabs.pulse).toMatchObject({ sessionId: 'schedule-goallead--goal-g1', name: 'Autopilot', metadata: { isViewOnly: false, isScheduledRun: false, isExecutionRun: true }, config: { inputText: 'Keep my draft' } })
 await askWorkflowPulse('Workflow/example', 'Why did revenue fall?')
 expect(state.send).toHaveBeenCalledWith({ tabId: 'pulse', message: 'Why did revenue fall?' })
 expect(state.open).toHaveBeenCalledTimes(1)
 expect(state.showChat).toHaveBeenCalledWith(true)
})
it('surfaces OFF/no-goal and lookup errors without opening or sending; account changes cancel the result', async () => {
 state.getLead.mockResolvedValueOnce({ success: true, conversation: { has_goal: false } })
 await expect(askWorkflowPulse('Workflow/example', 'Progress?')).rejects.toThrow('Turn Autopilot on')
 state.getLead.mockRejectedValueOnce(new Error('Connection unavailable'))
 await expect(openWorkflowPulseChatTab('Workflow/example')).rejects.toThrow('Connection unavailable')
 state.getLead.mockImplementationOnce(async () => { state.identity++; return { success: true, conversation: { has_goal: true, session_id: 'wrong-account' } } })
 await expect(openWorkflowPulseChatTab('Workflow/example')).rejects.toThrow('Chat account changed')
 expect(state.open).not.toHaveBeenCalled(); expect(state.send).not.toHaveBeenCalled()
})
