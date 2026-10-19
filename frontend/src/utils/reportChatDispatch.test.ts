// @vitest-environment happy-dom
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

const mocks = vi.hoisted(() => ({
  chat: {} as Record<string, any>,
  presets: {} as Record<string, any>,
  workflow: {} as Record<string, any>,
  select: vi.fn(() => true), activate: vi.fn(),
}))
vi.mock('../stores/useChatStore', () => ({ useChatStore: { getState: () => mocks.chat } }))
vi.mock('../stores/useGlobalPresetStore', () => ({ useGlobalPresetStore: { getState: () => mocks.presets } }))
vi.mock('../stores/useWorkflowStore', () => ({ useWorkflowStore: { getState: () => mocks.workflow } }))
vi.mock('./workflowNavigation', () => ({ selectWorkflowPreset: mocks.select }))
vi.mock('./activateTab', () => ({ activateTab: mocks.activate }))
import { sendReportHumanInputQuestionToChat } from './reportHumanInputChat'
import { openWorkspacePaneChatWithDraft, sendWorkspacePaneMessageToChat } from './workspacePaneChat'

function chat(tabId: string, extra = {}) {
  return { tabId, isStreaming: false, metadata: { mode: 'workflow', presetQueryId: 'one' }, ...extra }
}

beforeEach(() => {
  vi.useFakeTimers()
  vi.clearAllMocks()
  mocks.select.mockReturnValue(true)
  mocks.presets = { workflowPresets: [{ id: 'one', selectedFolder: { filepath: 'Workflow/one' } }], refreshPresets: vi.fn() }
  mocks.workflow = { setShowChatArea: vi.fn(), setShowWorkspacePane: vi.fn(), setFocusedPane: vi.fn() }
  mocks.chat = {
    chatTabs: {}, activeTabId: null,
    createChatTab: vi.fn(async () => {
      mocks.chat.chatTabs.fresh = chat('fresh')
      return 'fresh'
    }),
    getTab: (id: string) => mocks.chat.chatTabs[id],
    getActiveSessions: vi.fn(async () => []),
    getTabConfig: vi.fn(() => ({ queuedMessages: ['earlier request'], inputText: 'My unsent draft' })),
    setTabConfig: vi.fn(), setTabViewMode: vi.fn(), setAutoScroll: vi.fn(),
  }
})

describe('shared Ask in chat dispatch for reports', () => {
  it('prepares a dashboard draft without sending and preserves the user’s existing draft and queue', async () => {
    mocks.chat.chatTabs.existing = chat('existing')
    await openWorkspacePaneChatWithDraft({ workspacePath: 'Workflow/one', message: 'Help me create a dashboard.' })
    expect(mocks.chat.setTabConfig).toHaveBeenCalledExactlyOnceWith('existing', { inputText: 'Help me create a dashboard.\n\nMy unsent draft' })
    expect(mocks.chat.createChatTab).not.toHaveBeenCalled()
    expect(mocks.chat.getActiveSessions).not.toHaveBeenCalled()
    expect(mocks.activate).toHaveBeenCalledWith('existing')
  })
  it('appends to the running interactive chat queue without taking over a scheduled run', async () => {
    mocks.chat.chatTabs = {
      running: chat('running', { isStreaming: true }),
      scheduled: chat('scheduled', { isStreaming: true, metadata: { mode: 'workflow', presetQueryId: 'one', isScheduledRun: true } }),
      other: chat('other', { metadata: { mode: 'workflow', presetQueryId: 'other' } }),
    }
    const result = await sendWorkspacePaneMessageToChat({ workspacePath: 'Workflow/one', message: 'Apply finding 42' })
    expect(result).toEqual({ tabId: 'running', reused: true, queuedBehindRunningTurn: true })
    expect(mocks.chat.createChatTab).not.toHaveBeenCalled()
    expect(mocks.chat.setTabConfig).toHaveBeenCalledWith('running', { queuedMessages: ['earlier request', 'Apply finding 42'] })
    expect(mocks.chat.getActiveSessions).toHaveBeenCalledWith(true)
    expect(mocks.chat.setTabViewMode).toHaveBeenCalledWith('running', 'formatted')
    expect(mocks.chat.setTabConfig.mock.calls[0][1]).not.toHaveProperty('inputText')
    expect(mocks.activate).toHaveBeenCalledWith('running')
  })

  it('reuses an idle interactive chat rather than opening another', async () => {
    mocks.chat.chatTabs.existing = chat('existing')
    const result = await sendWorkspacePaneMessageToChat({ workspacePath: 'Workflow/one', message: 'Run visual QA' })
    expect(result).toEqual({ tabId: 'existing', reused: true, queuedBehindRunningTurn: false })
    expect(mocks.chat.createChatTab).not.toHaveBeenCalled()
    expect(mocks.chat.setTabConfig).toHaveBeenCalledWith('existing', { queuedMessages: ['earlier request', 'Run visual QA'] })
    expect(mocks.chat.getActiveSessions).not.toHaveBeenCalled()
    expect(mocks.chat.setTabViewMode).toHaveBeenCalledWith('existing', 'formatted')
  })

  it('creates a chat when this automation has none', async () => {
    const result = await sendWorkspacePaneMessageToChat({ workspacePath: 'Workflow/one', message: 'Run visual QA' })
    expect(result).toEqual({ tabId: 'fresh', reused: false, queuedBehindRunningTurn: false })
    expect(mocks.chat.createChatTab).toHaveBeenCalledExactlyOnceWith('Automation Builder', expect.objectContaining({ presetQueryId: 'one', phaseId: 'workflow-builder' }), undefined, undefined)
  })

  it('creates a chat when only a view-only schedule exists', async () => {
    mocks.chat.chatTabs.scheduled = chat('scheduled', { metadata: { mode: 'workflow', presetQueryId: 'one', isViewOnly: true } })
    await expect(sendWorkspacePaneMessageToChat({ workspacePath: 'Workflow/one', message: 'Apply finding 42' })).resolves.toMatchObject({ tabId: 'fresh', reused: false })
  })

  it('uses the same dispatcher for a pane that already owns a project chat tab', async () => {
    mocks.presets.workflowPresets = []
    mocks.chat.chatTabs.project = chat('project', { metadata: { mode: 'multi-agent' } })

    const result = await sendWorkspacePaneMessageToChat({ tabId: 'project', message: 'Explain this dashboard' })

    expect(result).toEqual({ tabId: 'project', reused: true, queuedBehindRunningTurn: false })
    expect(mocks.select).not.toHaveBeenCalled()
    expect(mocks.chat.createChatTab).not.toHaveBeenCalled()
    expect(mocks.chat.setTabConfig).toHaveBeenCalledWith('project', { queuedMessages: ['earlier request', 'Explain this dashboard'] })
    expect(mocks.activate).toHaveBeenCalledWith('project')
  })

  it('resolves a Crew pane through the durable project key instead of a stale browser tab', async () => {
    mocks.presets.workflowPresets = []
    mocks.chat.activeTabId = 'legacy'
    mocks.chat.chatTabs = {
      legacy: chat('legacy', { metadata: { mode: 'multi-agent', agentProfileId: 'work', agentProfileConversationKey: 'project-1:old-chat' } }),
      scheduled: chat('scheduled', { metadata: { mode: 'multi-agent', agentProfileId: 'work', agentProfileConversationKey: 'project-1', isScheduledRun: true } }),
      canonical: chat('canonical', { metadata: { mode: 'multi-agent', agentProfileId: 'work', agentProfileConversationKey: 'project-1', agentProfileBuilder: false } }),
    }

    const result = await sendWorkspacePaneMessageToChat({
      profileId: 'work', conversationKey: 'project-1', message: 'Apply this dashboard action',
    })

    expect(result).toEqual({ tabId: 'canonical', reused: true, queuedBehindRunningTurn: false })
    expect(mocks.chat.setTabConfig).toHaveBeenCalledWith('canonical', { queuedMessages: ['earlier request', 'Apply this dashboard action'] })
    expect(mocks.activate).toHaveBeenCalledWith('canonical')
    expect(mocks.chat.createChatTab).not.toHaveBeenCalled()
  })

  it('does not revive a legacy Crew chat when the canonical project chat is unavailable', async () => {
    mocks.chat.chatTabs.legacy = chat('legacy', {
      metadata: { mode: 'multi-agent', agentProfileId: 'work', agentProfileConversationKey: 'project-1:old-chat' },
    })
    await expect(sendWorkspacePaneMessageToChat({
      profileId: 'work', conversationKey: 'project-1', message: 'Apply this dashboard action',
    })).rejects.toThrow('project chat is not ready')
    expect(mocks.chat.setTabConfig).not.toHaveBeenCalled()
  })

  it('does not submit when the automation cannot be resolved', async () => {
    await expect(sendWorkspacePaneMessageToChat({ workspacePath: 'Workflow/missing', message: 'Apply finding 42' })).rejects.toThrow('Could not find')
    expect(mocks.chat.setTabConfig).not.toHaveBeenCalled()
    expect(mocks.chat.createChatTab).not.toHaveBeenCalled()
  })
  it('routes a human decision question through the same queue with its decision context', async () => {
    mocks.chat.chatTabs.existing = chat('existing', { isStreaming: true })
    await sendReportHumanInputQuestionToChat({
      workspacePath: 'Workflow/one', userQuestion: 'Why this option?',
      input: { id: 'decision-42', question: 'Approve release?', options: [], source: 'pulse' } as any,
    })
    const patch = mocks.chat.setTabConfig.mock.calls[0][1]
    expect(patch.queuedMessages).toHaveLength(2)
    expect(patch.queuedMessages[0]).toBe('earlier request')
    expect(patch.queuedMessages[1]).toContain('Decision ID: decision-42')
    expect(patch.queuedMessages[1]).toContain('Why this option?')
    expect(patch.queuedMessages[1]).toContain('Do not submit, dismiss, or mark the decision handled yet')
    expect(patch).not.toHaveProperty('inputText')
  })

})

afterEach(() => {
  vi.runOnlyPendingTimers()
  vi.useRealTimers()
})
