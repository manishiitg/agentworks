import { describe, expect, it } from 'vitest'
import { useChatStore, type ChatTab } from '../../stores/useChatStore'
import type { AgentProfileEffectiveChatModel } from '../../services/api-types'
import { applyWorkChatModels } from './useWorkChatModels'
import { setWorkProjectRuntimeSelection } from './workTabs'

const chat = (id: string, key: string, extra: Partial<NonNullable<ChatTab['metadata']>> = {}) => ({
  tabId: id, sessionId: id, metadata: { agentProfileId: 'work', agentProfileProjectId: 'crew', agentProfileConversationKey: key, ...extra },
} as ChatTab)
const effective = (key: string, own = false): AgentProfileEffectiveChatModel => ({ conversation_key: key, engine: 'cursor', provider: 'cursor-cli', model_id: 'auto', own_model: own, display_label: 'Cursor · auto' })

describe('server chat-model projection', () => {
  it('copies effective runtime, inheritance and edit permission directly from the server', () => {
    useChatStore.setState({ chatTabs: { main: chat('main', 'crew'), side: chat('side', 'crew:chat:review') } })
    applyWorkChatModels('work', 'crew', [effective('crew', true), effective('crew:chat:review')], false)
    const tabs = useChatStore.getState().chatTabs
    expect(tabs.main.metadata).toMatchObject({ agentProfileChatModelLabel: 'Cursor · auto', agentProfileChatModelOverride: true, agentProfileEngine: 'cursor', agentProfileModelID: 'auto', agentProfileCanOpenModels: false })
    expect(tabs.side.metadata).toMatchObject({ agentProfileChatModelOverride: false, agentProfileEngine: 'cursor', agentProfileModelID: 'auto' })
  })

  it('does not change runtime identity for label-only updates and clears stale labels on a project model change', () => {
    useChatStore.setState({ chatTabs: { main: chat('main', 'crew'), side: chat('side', 'crew:chat:review') } })
    const rows = [effective('crew', true), effective('crew:chat:review')]
    applyWorkChatModels('work', 'crew', rows, true)
    useChatStore.getState().setTabMetadata('side', { agentProfileRuntimeDirty: false })
    applyWorkChatModels('work', 'crew', [rows[0], { ...rows[1], display_label: 'Cursor · Auto' }], true)
    expect(useChatStore.getState().chatTabs.side.metadata).toMatchObject({ agentProfileChatModelLabel: 'Cursor · Auto', agentProfileRuntimeDirty: false, agentProfileModelID: 'auto' })
    setWorkProjectRuntimeSelection('crew', 'side', { engine: 'codex-cli', provider: 'codex-cli', modelId: 'gpt-6' })
    expect(useChatStore.getState().chatTabs.side.metadata?.agentProfileChatModelLabel).toBeUndefined()
    expect(useChatStore.getState().chatTabs.main.metadata?.agentProfileChatModelLabel).toBe('Cursor · auto')
  })

  it('never invents a model for missing records or modifies other profiles, history and published agents', () => {
    const protectedTabs = { other: chat('other', 'crew', { agentProfileId: 'code' }), history: chat('history', 'crew', { isViewOnly: true }), published: chat('published', 'crew:chat:agent-id'), missing: chat('missing', 'crew:chat:missing') }
    useChatStore.setState({ chatTabs: { main: chat('main', 'crew', { agentProfileChatModelOverride: true }), ...protectedTabs } })
    applyWorkChatModels('work', 'crew', [effective('crew')], true)
    const tabs = useChatStore.getState().chatTabs
    expect(tabs.main.metadata).toMatchObject({ agentProfileChatModelOverride: false, agentProfileCanOpenModels: true })
    for (const tab of Object.values(protectedTabs)) expect(tabs[tab.tabId]).toBe(tab)
  })

  it('keeps unavailable selections repairable without inventing a replacement runtime', () => {
    useChatStore.setState({ chatTabs: { main: chat('main', 'crew', { agentProfileEngine: 'cursor', agentProfileModelID: 'auto', agentProfileRuntimeDirty: false }) } })
    applyWorkChatModels('work', 'crew', [{ conversation_key: 'crew', engine: '', provider: '', own_model: true, display_label: 'Model unavailable', error: 'Choose an available model in Models.' }], true)
    expect(useChatStore.getState().chatTabs.main.metadata).toMatchObject({ agentProfileChatModelLabel: 'Model unavailable', agentProfileCanOpenModels: true, agentProfileEngine: 'cursor', agentProfileModelID: 'auto', agentProfileRuntimeDirty: false })
  })
})
