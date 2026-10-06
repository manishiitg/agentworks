import { describe, expect, it } from 'vitest'
import { shallow } from 'zustand/shallow'
import type { ChatTab } from '../../stores/useChatStore'
import { selectWorkChatTabIds } from './WorkSurface'
import { isWorkSideChatTab, workSideChatKey, workTabToKeepActive } from './workTabs'

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

  // PLAT-571: a side chat is a second full chat in the project. It stays the
  // active tab when chosen, but never becomes the primary, which keeps
  // Slack, WhatsApp, MCP, schedules and triggers.
  it('keeps a side chat active without making it the primary chat', () => {
    const side = { ...crewTab('t2', 'p1'), metadata: { agentProfileId: 'code', agentProfileProjectId: 'p1', agentProfileConversationKey: workSideChatKey('p1', 'a1b2c3d4') } } as unknown as ChatTab
    const state = { chatTabs: { t1: crewTab('t1', 'p1'), t2: side }, activeTabId: 't2' }
    expect(selectWorkChatTabIds(state, 'p1')).toEqual({ canonicalTabId: 't1', activeProjectTabId: 't2' })
    expect(isWorkSideChatTab(side, 'p1')).toBe(true)
    expect(isWorkSideChatTab(crewTab('t1', 'p1'), 'p1')).toBe(false)
    expect(isWorkSideChatTab(side, 'p2')).toBe(false)
  })

  // Owner, 2026-10-06: Ctrl+K from a Crew to a Code side chat landed on the
  // primary, because preparing the primary activates it.
  it('keeps the side chat the user picked when the primary chat is prepared', () => {
    const side = { ...crewTab('t2', 'p1'), tabId: 't2', metadata: { agentProfileId: 'code', agentProfileProjectId: 'p1', agentProfileConversationKey: workSideChatKey('p1', 'a1b2c3d4') } } as unknown as ChatTab
    const tabs = { t1: crewTab('t1', 'p1'), t2: side, t3: crewTab('t3', 'p2') }
    expect(workTabToKeepActive(tabs, 't2', 'p1', 't1')).toBe('t2')
    expect(workTabToKeepActive(tabs, 't1', 'p1', 't1')).toBeUndefined()
    expect(workTabToKeepActive(tabs, 't3', 'p1', 't1')).toBeUndefined()
    expect(workTabToKeepActive(tabs, 'gone', 'p1', 't1')).toBeUndefined()
  })

  it('still reacts when the active tab moves to another project', () => {
    const state = { chatTabs: { t1: crewTab('t1', 'p1'), t2: crewTab('t2', 'p2') }, activeTabId: 't2' }
    expect(selectWorkChatTabIds(state, 'p1')).toEqual({ canonicalTabId: 't1', activeProjectTabId: undefined })
    expect(selectWorkChatTabIds(state, undefined)).toEqual({ canonicalTabId: undefined, activeProjectTabId: undefined })
  })
})
