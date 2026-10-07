// @vitest-environment happy-dom
import React, { act } from 'react'
import { createRoot } from 'react-dom/client'
import { describe, expect, it, vi } from 'vitest'
import type { ReportHumanInput } from '../../services/api-types'

vi.mock('../../services/api', () => ({
  agentApi: { answerReportHumanInput: vi.fn(async () => ({ success: true, input: {}, apply_message: 'Apply decision goal-check-1' })) },
}))
vi.mock('../../stores/useChatStore', () => ({ useChatStore: { getState: () => ({ addToast: vi.fn() }) } }))
vi.mock('../../hooks/useLiveRefetch', () => ({ useLiveRefetch: () => {} }))
vi.mock('../../utils/reportHumanInputChat', () => ({ openReportHumanInputAnswerInChat: vi.fn() }))
vi.mock('../../utils/workspacePaneChat', () => ({ sendWorkspacePaneMessageToChat: vi.fn(async () => ({})) }))

import { agentApi } from '../../services/api'
import { sendWorkspacePaneMessageToChat } from '../../utils/workspacePaneChat'
import { NeedsYouCard } from './GoalLeadPanel'

// PLAT-697 phase 3: the Goal Lead recommends, the owner confirms with one click.
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
      expect(text).toContain('Goal Lead recommends: Resume growth runs')
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
