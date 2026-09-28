// @vitest-environment happy-dom
import React, { act } from 'react'
import { createRoot } from 'react-dom/client'
import { describe, expect, it, vi } from 'vitest'
import type { ReportHumanInput } from '../../services/api-types'

vi.mock('../../services/api', () => ({ agentApi: { listReportHumanInputs: vi.fn() } }))
vi.mock('../../stores/useChatStore', () => ({ useChatStore: { getState: () => ({ addToast: vi.fn() }) } }))
vi.mock('./reportWidgets/tableHelpers', () => ({ useContainerSizeTier: () => [null, 'desktop'] }))
vi.mock('../../utils/reportHumanInputChat', () => ({
  delegateReportHumanInputActionToChat: vi.fn(), sendReportHumanInputQuestionToChat: vi.fn(),
  openReportHumanInputAnswerInChat: vi.fn(), openReportHumanInputQuestionInChat: vi.fn(),
  sendReportHumanInputAnswerToChat: vi.fn(async () => ({ tabId: 't', reused: true, queuedBehindRunningTurn: false })),
}))
const liveFeedListeners = vi.hoisted(() => [] as (() => void)[])
vi.mock('../../services/liveFeed', () => ({
  liveFeed: {
    getStatus: () => 'live',
    onStatus: () => () => {},
    subscribe: (_kinds: string[], _workflow: string | null, onChange: () => void) => {
      liveFeedListeners.push(onChange)
      return () => {}
    },
  },
}))
vi.mock('../ui/PlainMarkdown', () => ({ PlainMarkdown: ({ content }: { content: string }) => <span>{content}</span> }))

import { agentApi } from '../../services/api'
import { openReportHumanInputAnswerInChat, sendReportHumanInputAnswerToChat } from '../../utils/reportHumanInputChat'
import { ReportHumanInputPanel } from './ReportHumanInputPanel'

describe('decision card refresh from the server live feed', () => {
  it('moves a saved answer out of pending on a human_inputs notice, without the chat or a manual refresh', async () => {
    Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
    const workspace = 'Workflow/example'
    const input = { id: 'decision-1', workspace_path: workspace, source: 'technical_review',
      status: 'pending', question: 'Approve measurement?', options: [{ id: 'approve', title: 'Approve' }],
      allow_free_text: false, created_at: '2026-09-05T00:00:00Z' } as ReportHumanInput
    vi.mocked(agentApi.listReportHumanInputs).mockResolvedValue({ success: true, inputs: [input] })
    const container = document.createElement('div')
    document.body.append(container)
    const root = createRoot(container)
    try {
      await act(async () => root.render(<ReportHumanInputPanel workspacePath={workspace} />))
      expect(container.textContent).toContain('Needs your decision')
      const answered = { ...input, status: 'answered' as const, selected_option_id: 'approve' }
      vi.mocked(agentApi.listReportHumanInputs).mockResolvedValue({ success: true, inputs: [answered] })
      await act(async () => { liveFeedListeners.forEach(notify => notify()) })
      expect(container.textContent).not.toContain('Needs your decision')
      expect(container.textContent).not.toContain('Save answer')
      expect(container.textContent).toContain('Approve measurement?')
    } finally {
      await act(async () => root.unmount())
      container.remove()
    }
  })
})

describe('decision option click', () => {
  it('sends the chosen answer on a confirmed second click, like Ask AI', async () => {
    Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
    const workspace = 'Workflow/example'
    const input = { id: 'decision-2', workspace_path: workspace, source: 'technical_review',
      status: 'pending', question: 'Which Slack bot?', options: [{ id: 'own', title: "This workflow's own bot" }, { id: 'shared', title: 'Shared platform bot' }],
      allow_free_text: false, created_at: '2026-09-26T00:00:00Z' } as ReportHumanInput
    vi.mocked(agentApi.listReportHumanInputs).mockResolvedValue({ success: true, inputs: [input] })
    const container = document.createElement('div')
    document.body.append(container)
    const root = createRoot(container)
    try {
      await act(async () => root.render(<ReportHumanInputPanel workspacePath={workspace} />))
      const option = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes("This workflow's own bot"))
      expect(option).toBeTruthy()
      // First click arms (nothing sent); a second click right away is the
      // tail of a double-click and is ignored; a deliberate second click sends.
      const now = vi.spyOn(Date, 'now').mockReturnValue(1_000)
      await act(async () => { option!.click() })
      expect(sendReportHumanInputAnswerToChat).not.toHaveBeenCalled()
      expect(container.textContent).toContain('Click again to send')
      await act(async () => { option!.click() })
      expect(sendReportHumanInputAnswerToChat).not.toHaveBeenCalled()
      now.mockReturnValue(2_000)
      await act(async () => { option!.click() })
      now.mockRestore()
      expect(sendReportHumanInputAnswerToChat).toHaveBeenCalledWith(expect.objectContaining({ workspacePath: workspace, option: { id: 'own', title: "This workflow's own bot" } }))
      expect(openReportHumanInputAnswerInChat).not.toHaveBeenCalled()
    } finally {
      await act(async () => root.unmount())
      container.remove()
    }
  })
})
