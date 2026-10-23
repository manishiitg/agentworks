import { describe, expect, it } from 'vitest'
import type { ChatTab } from '../../stores/useChatStore'
import { selectWorkflowTabsForStrip } from './workflowTabStripSelection'

function tab(tabId: string, overrides: Partial<ChatTab> = {}): ChatTab {
  return {
    tabId,
    name: tabId,
    sessionId: `${tabId}-session`,
    createdAt: 1,
    isStreaming: false,
    config: {},
    metadata: { mode: 'workflow', presetQueryId: 'twitter-automation' },
    ...overrides,
  } as ChatTab
}

describe('selectWorkflowTabsForStrip', () => {
  it('does not keep the previous workflow title or running tab after a switch', () => {
    const previous = tab('previous', {
      isStreaming: true,
      metadata: { mode: 'workflow', presetQueryId: 'previous-workflow', phaseId: 'workflow-builder' },
    })
    const current = tab('current', {
      metadata: { mode: 'workflow', presetQueryId: 'current-workflow', phaseId: 'workflow-builder' },
    })
    const previousRun = tab('previous-run', {
      metadata: { mode: 'workflow', presetQueryId: 'previous-workflow', isViewOnly: true },
    })
    expect(selectWorkflowTabsForStrip([previous, current, previousRun], 'previous', 'current-workflow', {}))
      .toEqual([current])
    expect(selectWorkflowTabsForStrip([previous], 'previous', 'current-workflow', {}))
      .toEqual([])
  })

  it('retains an unbound active run while its workflow metadata resolves', () => {
    const restoringRun = tab('restoring-run', {
      metadata: { mode: 'workflow', isViewOnly: true, isScheduledRun: true },
    })
    expect(selectWorkflowTabsForStrip([restoringRun], 'restoring-run', 'current-workflow', {}))
      .toEqual([restoringRun])
  })

  it('keeps an opened schedule visible after Chat receives focus', () => {
    const chat = tab('chat', {
      name: 'Automation Builder',
      metadata: { mode: 'workflow', presetQueryId: 'twitter-automation', phaseId: 'workflow-builder' },
    })
    const schedule = tab('schedule', {
      name: 'Daily SaaS Builder Growth x6',
      createdAt: 2,
      metadata: { mode: 'workflow', presetQueryId: 'twitter-automation', isViewOnly: true, isScheduledRun: true },
    })

    expect(selectWorkflowTabsForStrip([chat, schedule], 'chat', 'twitter-automation', {}))
      .toEqual([chat, schedule])
  })

  it('collapses duplicate interactive chats without hiding run lanes', () => {
    const olderChat = tab('old-chat', {
      name: 'Automation Builder',
      createdAt: 1,
      metadata: { mode: 'workflow', presetQueryId: 'twitter-automation', phaseId: 'workflow-builder' },
    })
    const currentChat = tab('current-chat', {
      name: 'Automation Builder',
      createdAt: 2,
      metadata: { mode: 'workflow', presetQueryId: 'twitter-automation', phaseId: 'workflow-builder' },
    })
    const schedule = tab('schedule', {
      createdAt: 3,
      metadata: { mode: 'workflow', presetQueryId: 'twitter-automation', isViewOnly: true, isScheduledRun: true },
    })

    expect(selectWorkflowTabsForStrip([olderChat, currentChat, schedule], 'current-chat', 'twitter-automation', {}))
      .toEqual([currentChat, schedule])
  })

  // PLAT-665: New chat leaves the active Chat blank. A hidden older chat with
  // events must not take the strip over, or focus jumps to that old chat.
  it('keeps a blank active Chat after New chat instead of an older chat with events', () => {
    const builder = { mode: 'workflow' as const, presetQueryId: 'twitter-automation', phaseId: 'workflow-builder' }
    const olderChat = tab('old-chat', { name: 'Automation Builder', createdAt: 1, metadata: builder })
    const freshChat = tab('fresh-chat', { name: 'Automation Builder', createdAt: 2, metadata: builder })
    const events = { 'old-chat-session': [{ id: 'e1', type: 'user_message' }] } as unknown as Parameters<typeof selectWorkflowTabsForStrip>[3]

    expect(selectWorkflowTabsForStrip([olderChat, freshChat], 'fresh-chat', 'twitter-automation', events))
      .toEqual([freshChat])
  })
})


it('keeps every registered owner tab beside function and schedule history', () => {
  const own = { tabId: 'own', sessionId: 's-own', name: 'Builder', createdAt: 1, metadata: { mode: 'workflow', presetQueryId: 'wf', phaseId: 'workflow-builder', workflowOwnerTabID: 'wf:chat:own', agentProfileTabCanWrite: true } } as ChatTab
  const other = { ...own, tabId: 'other', sessionId: 's-other', metadata: { ...own.metadata, workflowOwnerTabID: 'wf:chat:other', agentProfileTabCanWrite: false } } as ChatTab
  const run = { ...own, tabId: 'run', sessionId: 's-run', metadata: { mode: 'workflow', presetQueryId: 'wf', isViewOnly: true, isExecutionRun: true } } as ChatTab
  expect(selectWorkflowTabsForStrip([own, other, run], 'other', 'wf', {}).map(tab => tab.tabId)).toEqual(['own', 'other', 'run'])
})
