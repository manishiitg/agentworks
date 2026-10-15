// @vitest-environment happy-dom
import { describe, expect, it, vi } from 'vitest'
const mocks = vi.hoisted(() => ({ create: vi.fn().mockResolvedValue('tab'), metadata: vi.fn(), view: vi.fn(), hydrate: vi.fn().mockResolvedValue({}), execution: vi.fn().mockResolvedValue({}), activate: vi.fn() }))
vi.mock('../stores/useChatStore', () => ({ useChatStore: { getState: () => ({ chatTabs: {}, createChatTab: mocks.create, setTabMetadata: mocks.metadata, setTabViewMode: mocks.view }) } }))
vi.mock('./sessionRestore', () => ({ hydrateTabEvents: mocks.hydrate }))
vi.mock('./executionConversationRestore', () => ({ hydrateExecutionConversation: mocks.execution }))
vi.mock('./activateTab', () => ({ activateTab: mocks.activate }))
import { restoreWorkspaceChat } from './restoreWorkspaceChat'
describe('workspace chat restoration', () => {
  it('restores a schedule as the same read-only execution tab using its proper transcript reader', async () => {
    await restoreWorkspaceChat({ surface: 'agentworks', id: 'finance', workspace_path: 'Workflow/finance', session_id: 'schedule-finance-1', chat: { name: 'Morning briefing · 7:30', read_only: true, execution_run: true, scheduled_run: true, scheduled_job_name: 'Morning briefing', view_mode: 'tree' } }, new AbortController().signal)
    expect(mocks.create).toHaveBeenCalledWith('Morning briefing · 7:30', expect.objectContaining({ mode: 'workflow', presetQueryId: 'finance', isExecutionRun: true, isScheduledRun: true, isViewOnly: true, scheduledJobName: 'Morning briefing' }), 'schedule-finance-1')
    expect(mocks.execution).toHaveBeenCalledWith('schedule-finance-1', 'Workflow/finance')
    expect(mocks.hydrate).not.toHaveBeenCalled()
    expect(mocks.view).toHaveBeenCalledWith('tab', 'tree')
    expect(mocks.activate).toHaveBeenCalledWith('tab')
  })
})
