import { describe, expect, it } from 'vitest'
import { shallow } from 'zustand/shallow'
import type { ChatTab } from '../../stores/useChatStore'
import { selectWorkChatTabIds } from './WorkSurface'

function crewTab(tabId: string, projectId: string, inputText = ''): ChatTab {
  return {
    tabId,
    metadata: { agentProfileId: 'work', agentProfileProjectId: projectId, agentProfileConversationKey: projectId },
    config: { inputText },
  } as unknown as ChatTab
}

describe('Work surface chat-tab selection', () => {
  it('ignores the chat draft, so typing does not re-render the workspace side', () => {
    const before = { chatTabs: { t1: crewTab('t1', 'p1', 'h') }, activeTabId: 't1' }
    // What a debounced keystroke does: the tab's config (and the map) are new objects.
    const after = { chatTabs: { t1: crewTab('t1', 'p1', 'hello') }, activeTabId: 't1' }
    const a = selectWorkChatTabIds(before, 'p1')
    const b = selectWorkChatTabIds(after, 'p1')
    expect(a).toEqual({ canonicalTabId: 't1', activeProjectTabId: 't1' })
    expect(shallow(a, b)).toBe(true)
  })

  it('still reacts when the active tab moves to another project', () => {
    const state = { chatTabs: { t1: crewTab('t1', 'p1'), t2: crewTab('t2', 'p2') }, activeTabId: 't2' }
    expect(selectWorkChatTabIds(state, 'p1')).toEqual({ canonicalTabId: 't1', activeProjectTabId: undefined })
    expect(selectWorkChatTabIds(state, undefined)).toEqual({ canonicalTabId: undefined, activeProjectTabId: undefined })
  })
})
