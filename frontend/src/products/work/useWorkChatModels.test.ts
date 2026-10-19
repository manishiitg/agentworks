import { describe, expect, it } from 'vitest'
import { useChatStore, type ChatTab } from '../../stores/useChatStore'
import { applyWorkChatModels } from './useWorkChatModels'
import { setWorkProjectRuntimeSelection } from './workTabs'

const chat = (id: string, key: string, extra: Partial<NonNullable<ChatTab['metadata']>> = {}) => ({
  tabId: id, sessionId: id, metadata: { agentProfileId: 'work', agentProfileProjectId: 'crew', agentProfileConversationKey: key, ...extra },
} as ChatTab)

describe('server chat-model projection', () => {
  it('projects server labels without changing runtime identity or marking display-only updates dirty', () => {
    useChatStore.setState({ chatTabs: { main: chat('main', 'crew'), side: chat('side', 'crew:chat:review') } })
    const models = [{ conversation_key: 'crew', engine: 'cursor-cli', model_id: 'auto', display_label: 'Cursor · auto' }]
    const fallback = { provider: 'claude-code', modelId: 'claude-sonnet-5-5' }
    applyWorkChatModels('work', 'crew', models, fallback, 'Claude · Sonnet 5.5')
    let tabs = useChatStore.getState().chatTabs
    expect(tabs.main.metadata).toMatchObject({ agentProfileChatModelLabel: 'Cursor · auto', agentProfileChatModelOverride: true, agentProfileModelID: 'auto' })
    expect(tabs.side.metadata).toMatchObject({ agentProfileChatModelLabel: 'Claude · Sonnet 5.5', agentProfileChatModelOverride: false, agentProfileModelID: 'claude-sonnet-5-5' })
    useChatStore.getState().setTabMetadata('side', { agentProfileRuntimeDirty: false })
    applyWorkChatModels('work', 'crew', models, fallback, 'Claude · Sonnet')
    tabs = useChatStore.getState().chatTabs
    expect(tabs.side.metadata).toMatchObject({ agentProfileChatModelLabel: 'Claude · Sonnet', agentProfileRuntimeDirty: false, agentProfileModelID: 'claude-sonnet-5-5' })
    setWorkProjectRuntimeSelection('crew', 'side', { engine: 'codex-cli', provider: 'codex-cli', modelId: 'gpt-6' })
    expect(useChatStore.getState().chatTabs.side.metadata?.agentProfileChatModelLabel).toBeUndefined()
    expect(useChatStore.getState().chatTabs.main.metadata?.agentProfileChatModelLabel).toBe('Cursor · auto')
  })

  it('keeps an explicit model on main while default changes update only inheriting chats', () => {
    useChatStore.setState({ chatTabs: { main: chat('main', 'crew'), side: chat('side', 'crew:chat:review') } })
    applyWorkChatModels('work', 'crew', [{ conversation_key: 'crew', engine: 'muse-cli', model_id: 'own-model' }], { provider: 'codex-cli', modelId: 'crew-model' })
    setWorkProjectRuntimeSelection('crew', 'main', { engine: 'claude-code', provider: 'claude-code', modelId: 'new-crew-model' })
    expect(useChatStore.getState().chatTabs.main.metadata).toMatchObject({ agentProfileChatModelOverride: true, agentProfileEngine: 'muse-cli', agentProfileModelID: 'own-model' })
    expect(useChatStore.getState().chatTabs.side.metadata).toMatchObject({ agentProfileChatModelOverride: false, agentProfileModelID: 'new-crew-model' })
  })

  it('clears an override from server data and leaves other profiles, read-only and published agents untouched', () => {
    const protectedTabs = { other: chat('other', 'crew', { agentProfileId: 'code' }), history: chat('history', 'crew', { isViewOnly: true }), published: chat('published', 'crew:chat:agent-id') }
    useChatStore.setState({ chatTabs: { main: chat('main', 'crew', { agentProfileChatModelOverride: true }), ...protectedTabs } })
    applyWorkChatModels('work', 'crew', [], { provider: 'codex-cli', modelId: 'default-model', connectionId: 'own-account' })
    const tabs = useChatStore.getState().chatTabs
    expect(tabs.main.metadata).toMatchObject({ agentProfileChatModelOverride: false, agentProfileModelID: 'default-model', agentProfileConnectionID: 'own-account' })
    for (const tab of Object.values(protectedTabs)) expect(tabs[tab.tabId]).toBe(tab)
  })
})
