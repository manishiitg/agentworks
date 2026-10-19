import { describe, expect, it } from 'vitest'
import { useChatStore, type ChatTab } from '../../stores/useChatStore'
import { applyMainChatChange, canMakeMainChat } from './mainChatSwap'

const chat = (tabId: string, key: string, sessionId: string) => ({ tabId, sessionId, name: tabId, config: { inputText: tabId }, metadata: { agentProfileId: 'work', agentProfileProjectId: 'crew', agentProfileConversationKey: key, agentProfileModelID: `${tabId}-model` } } as ChatTab)

describe('main chat pointer', () => {
  it('changes only main role and activation, retaining every chat ID, session, name, draft and model', () => {
    const main = chat('original', 'crew:chat:root-a', 'original-session')
    const review = chat('review', 'crew:chat:reviewer', 'review-session')
    const events = { 'original-session': [{ type: 'user_message' }], 'review-session': [{ type: 'tool_call_end' }] }
    useChatStore.setState({ chatTabs: { original: main, review }, tabEvents: events as never, activeTabId: 'original' })
    applyMainChatChange('crew', { main: { session_id: review.sessionId!, conversation_id: 'review-conv', conversation_key: 'crew:chat:reviewer' }, previous_main: { session_id: main.sessionId!, conversation_id: 'original-conv', conversation_key: 'crew:chat:root-a' } })
    const state = useChatStore.getState()
    for (const before of [main, review]) {
      const after = state.chatTabs[before.tabId]
      expect(after.sessionId).toBe(before.sessionId)
      expect(after.name).toBe(before.name)
      expect(after.config).toBe(before.config)
      expect(after.metadata?.agentProfileModelID).toBe(before.metadata?.agentProfileModelID)
      expect(after.metadata?.agentProfileConversationKey).toBe(before.metadata?.agentProfileConversationKey)
    }
    expect(state.activeTabId).toBe('review')
    expect(state.chatTabs.review.metadata?.agentProfileIsMainChat).toBe(true)
    expect(state.chatTabs.original.metadata?.agentProfileIsMainChat).toBe(false)
    expect(state.tabEvents).toBe(events)
  })
  it('keeps function and publication slots out of main selection', () => {
    for (const key of ['crew:chat:fn.run', 'crew:chat:agent-id', 'other:chat:reviewer', 'crew']) expect(canMakeMainChat(key, 'crew')).toBe(false)
    expect(canMakeMainChat('crew:chat:reviewer', 'crew')).toBe(true)
  })
})
