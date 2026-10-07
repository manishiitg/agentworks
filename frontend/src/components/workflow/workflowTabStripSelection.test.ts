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
