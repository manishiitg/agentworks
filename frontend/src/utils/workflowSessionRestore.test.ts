import { beforeEach, expect, it } from 'vitest'
import { useChatStore, type ChatTab } from '../stores/useChatStore'
import { findOpenWorkflowChatTab } from './workflowSessionRestore'

const tab = (tabId: string, metadata: ChatTab['metadata'], patch: Partial<ChatTab> = {}) =>
  ({ tabId, sessionId: tabId, createdAt: 1, metadata, ...patch }) as ChatTab

beforeEach(() => useChatStore.setState({ chatTabs: {} }))

// Reopening a workflow (e.g. from the preset dropdown) must find the chat the
// person already has, including a streaming one, not start a blank one.
it('finds the same streaming workflow chat each time and ignores run views and other workflows', () => {
  const chat = tab('chat', { mode: 'workflow', presetQueryId: 'wf' }, { isStreaming: true })
  useChatStore.setState({ chatTabs: {
    chat,
    run: tab('run', { mode: 'workflow', presetQueryId: 'wf', isViewOnly: true }, { lastAccessedAt: 9 }),
    other: tab('other', { mode: 'workflow', presetQueryId: 'other' }),
  } })
  expect(findOpenWorkflowChatTab('wf')?.tabId).toBe('chat')
  expect(findOpenWorkflowChatTab('wf')?.tabId).toBe('chat')
  expect(findOpenWorkflowChatTab('none')).toBeUndefined()
})
