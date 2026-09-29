import { describe, expect, it } from 'vitest'
import type { ChatTab } from '../../stores/useChatStore'
import { useChatStore } from '../../stores/useChatStore'
import {
  applyWorkProjectRuntimeSelection,
  findCanonicalWorkProjectTab,
  markWorkProjectRuntimeDirty,
  setWorkProjectRuntimeSelection,
} from './workTabs'

function tab(overrides: Partial<ChatTab> & Pick<ChatTab, 'tabId'>): ChatTab {
  return {
    name: 'Chat', sessionId: overrides.tabId, isStreaming: false, isCompleted: false,
    hasRunningBgAgents: false, isSyntheticTurn: false, canSteer: false, hideToolCalls: true,
    viewMode: 'formatted', config: {} as ChatTab['config'], createdAt: 1, lastAccessedAt: 1,
    lastViewedEventCount: 0, lastViewedEventCounts: { micro: 0 },
    metadata: { mode: 'multi-agent', agentProfileId: 'work', agentProfileProjectId: 'project-1' },
    ...overrides,
  }
}

describe('findCanonicalWorkProjectTab', () => {
  it('uses the durable server session across legacy builder and multi-chat state', () => {
    const legacyBuilder = tab({
      tabId: 'builder', sessionId: 'canonical-session',
      metadata: { mode: 'multi-agent', agentProfileId: 'work', agentProfileProjectId: 'project-1', agentProfileBuilder: true },
    })
    const priorChat = tab({
      tabId: 'prior', sessionId: 'prior-session',
      metadata: { mode: 'multi-agent', agentProfileId: 'work', agentProfileProjectId: 'project-1', agentProfileConversationKey: 'project-1:prior' },
    })
    expect(findCanonicalWorkProjectTab({ builder: legacyBuilder, prior: priorChat }, 'project-1', 'canonical-session')?.tabId).toBe('builder')
  })

  it('never adopts a matching session from another project', () => {
    const other = tab({
      tabId: 'other', sessionId: 'canonical-session',
      metadata: { mode: 'multi-agent', agentProfileId: 'work', agentProfileProjectId: 'project-2' },
    })
    expect(findCanonicalWorkProjectTab({ other }, 'project-1', 'canonical-session')).toBeUndefined()
  })

  it('never adopts a read-only history projection as the canonical chat', () => {
    const history = tab({
      tabId: 'history', sessionId: 'canonical-session',
      metadata: {
        mode: 'multi-agent', agentProfileId: 'work', agentProfileProjectId: 'project-1',
        agentProfileConversationKey: 'project-1:history:canonical-session', isViewOnly: true,
      },
    })
    expect(findCanonicalWorkProjectTab({ history }, 'project-1', 'canonical-session')).toBeUndefined()
  })
})

describe('persistent Work runtime', () => {
  it('marks only the selected project conversation dirty after context changes', () => {
    const current = tab({ tabId: 'current' })
    const other = tab({ tabId: 'other', metadata: { mode: 'multi-agent', agentProfileId: 'work', agentProfileProjectId: 'project-2' } })
    useChatStore.setState({ chatTabs: { current, other } })
    markWorkProjectRuntimeDirty('project-1')
    expect(useChatStore.getState().chatTabs.current.metadata?.agentProfileRuntimeDirty).toBe(true)
    expect(useChatStore.getState().chatTabs.other.metadata?.agentProfileRuntimeDirty).toBeUndefined()
  })

  it('changes provider on the same durable conversation', () => {
    const current = tab({ tabId: 'current', sessionId: 'persistent-session' })
    const other = tab({ tabId: 'other', metadata: { mode: 'multi-agent', agentProfileId: 'work', agentProfileProjectId: 'project-2' } })
    useChatStore.setState({ chatTabs: { current, other } })
    setWorkProjectRuntimeSelection('project-1', 'current', {
      engine: 'claude-code', provider: 'claude-code', connectionId: 'personal', modelId: 'claude-sonnet-5-5',
    })
    const tabs = useChatStore.getState().chatTabs
    expect(tabs.current.sessionId).toBe('persistent-session')
    expect(tabs.current.metadata).toMatchObject({
      agentProfileEngine: 'claude-code', agentProfileConnectionID: 'personal',
      agentProfileModelID: 'claude-sonnet-5-5', agentProfileRuntimeDirty: true,
    })
    expect(tabs.other.metadata?.agentProfileEngine).toBeUndefined()
  })

  it('uses the composer tab carried by the event instead of the global active tab', () => {
    const current = tab({ tabId: 'current' })
    const globallyActive = tab({ tabId: 'global', metadata: { mode: 'multi-agent', agentProfileId: 'work', agentProfileProjectId: 'project-2' } })
    useChatStore.setState({ activeTabId: 'global', chatTabs: { current, global: globallyActive } })
    expect(applyWorkProjectRuntimeSelection('project-1', null, {
      profileId: 'work', tabId: 'current', engine: 'cursor-cli', modelId: 'cursor-auto',
    })).toBe(true)
    expect(useChatStore.getState().chatTabs.current.metadata?.agentProfileEngine).toBe('cursor-cli')
    expect(useChatStore.getState().chatTabs.global.metadata?.agentProfileEngine).toBeUndefined()
  })
})
