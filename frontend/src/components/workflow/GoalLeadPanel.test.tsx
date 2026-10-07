// @vitest-environment happy-dom
import React, { act } from 'react'
import { createRoot } from 'react-dom/client'
import { describe, expect, it, vi } from 'vitest'
import type { GoalLeadResponse, ReportHumanInput } from '../../services/api-types'

vi.mock('../../services/api', () => ({
  agentApi: {
    answerReportHumanInput: vi.fn(async () => ({ success: true, input: {}, apply_message: 'Apply decision goal-check-1' })),
    listReportHumanInputs: vi.fn(async () => ({ success: true, inputs: [] })),
    getGoalLead: vi.fn(),
    saveGoalMemory: vi.fn(async () => ({ success: true })),
    sendGoalLeadMessage: vi.fn(async () => ({ success: true })),
    updateGoalLeadFocusArea: vi.fn(async () => ({ success: true })),
  },
}))
vi.mock('../../stores/useChatStore', () => ({ useChatStore: { getState: () => ({ addToast: vi.fn() }) } }))
vi.mock('../../hooks/useLiveRefetch', () => ({ useLiveRefetch: () => {} }))
vi.mock('../../utils/reportHumanInputChat', () => ({ openReportHumanInputAnswerInChat: vi.fn() }))
vi.mock('../../utils/workspacePaneChat', () => ({ sendWorkspacePaneMessageToChat: vi.fn(async () => ({})) }))
vi.mock('../../utils/pulseChatTab', () => ({ openPulseChatTab: vi.fn(async () => {}) }))

import { agentApi } from '../../services/api'
import { sendWorkspacePaneMessageToChat } from '../../utils/workspacePaneChat'
import { openPulseChatTab } from '../../utils/pulseChatTab'
import { GoalLeadPanel, NeedsYouCard } from './GoalLeadPanel'

// PLAT-697 phase 3: the Pulse recommends, the owner confirms with one click.
describe('Needs you card', () => {
  it('shows the recommendation, waiting time and what it blocks, and Accept answers with the recommended option', async () => {
    Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
    const workspace = 'Workflow/substack'
    const input = {
      id: 'goal-check-1', workspace_path: workspace, source: 'strategic_review', priority: 'medium', status: 'pending',
      question: 'Resume the growth runs?', allow_free_text: false,
      options: [{ id: 'resume', title: 'Resume growth runs' }, { id: 'wait', title: 'Keep them paused' }],
      created_at: new Date(Date.now() - 3 * 24 * 3_600_000).toISOString(), updated_at: '',
      recommendation: {
        input_id: 'goal-check-1', option_id: 'resume', why: 'Growth has not run for 11 days.', confidence: 'high',
        blocks: "Friday's growth run", recommended_by: 'pulse', recommended_at: '2026-10-07T00:00:00Z',
      },
    } as ReportHumanInput
    const container = document.createElement('div')
    document.body.append(container)
    const root = createRoot(container)
    try {
      await act(async () => root.render(<NeedsYouCard input={input} workspacePath={workspace} />))
      const text = container.textContent || ''
      expect(text).toContain('Pulse recommends: Resume growth runs')
      expect(text).toContain('Waiting 3 days')
      expect(text).toContain("Blocks: Friday's growth run")
      expect(text).toContain('Change')

      const accept = Array.from(container.querySelectorAll('button')).find(button => button.textContent === 'Accept')
      expect(accept).toBeTruthy()
      await act(async () => { accept!.click() })
      expect(agentApi.answerReportHumanInput).toHaveBeenCalledWith(workspace, 'goal-check-1', { selected_option_id: 'resume' })
      // The answer is applied in the Builder chat, where the owner can watch.
      expect(sendWorkspacePaneMessageToChat).toHaveBeenCalledWith({ workspacePath: workspace, message: 'Apply decision goal-check-1' })
    } finally {
      await act(async () => root.unmount())
      container.remove()
    }
  })
})

// Owner, 2026-10-08: the Pulse tab does not show Pulse's conversation (its own
// "<workflow> Pulse" chat tab does); "Talk to Pulse" sends straight to Pulse and
// opens that tab. Focus-area proposals are still confirmed with one click.
describe('Pulse tab: focus areas and Talk to Pulse', () => {
  it('confirms a proposed focus area, shows no conversation, and sends to Pulse then opens its chat tab', async () => {
    Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
    const workspace = 'Workflow/substack'
    vi.mocked(agentApi.getGoalLead).mockResolvedValue({
      success: true, memory: '', memory_path: 'Workflow/substack/memory/goal.md', decision_log: [],
      conversation: {
        has_goal: true, busy: false, session_id: 'schedule-goallead--abc-g1',
        messages: [{ id: 'm1', at: '2026-10-07T09:00:00Z', role: 'check', text: 'Not measured for 20 days; I asked you to resume the growth runs.' }],
      },
      focus_areas: [{ id: 'FA-1', text: 'Clear the drafts waiting for approval', status: 'proposed', end_date: '2026-10-21', check: 'drafts waiting 3 -> 0', why: 'Three drafts have waited a week.', proposed_by: 'goal_lead' }],
    } as GoalLeadResponse)
    const container = document.createElement('div')
    document.body.append(container)
    const root = createRoot(container)
    try {
      await act(async () => root.render(<GoalLeadPanel workspacePath={workspace} />))
      const text = container.textContent || ''
      expect(text).not.toContain('Not measured for 20 days')
      expect(text).toContain('Pulse proposes')
      expect(text).toContain('drafts waiting 3 -> 0')

      const confirm = Array.from(container.querySelectorAll('button')).find(button => button.textContent === 'Confirm')
      expect(confirm).toBeTruthy()
      await act(async () => { confirm!.click() })
      expect(agentApi.updateGoalLeadFocusArea).toHaveBeenCalledWith(workspace, { action: 'confirm', id: 'FA-1' })

      expect(text).toContain('Replies appear in the Pulse chat tab')
      const input = container.querySelector('textarea[aria-label="Message to Pulse"]') as HTMLTextAreaElement
      expect(input).toBeTruthy()
      await act(async () => {
        Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, 'value')!.set!.call(input, 'Why did you pause the growth runs?')
        input.dispatchEvent(new Event('input', { bubbles: true }))
      })
      const send = Array.from(container.querySelectorAll('button')).find(button => button.textContent === 'Send')
      await act(async () => { send!.click() })
      expect(agentApi.sendGoalLeadMessage).toHaveBeenCalledWith(workspace, 'Why did you pause the growth runs?')
      expect(openPulseChatTab).toHaveBeenCalledWith(workspace, 'schedule-goallead--abc-g1')
    } finally {
      await act(async () => root.unmount())
      container.remove()
    }
  })
})
