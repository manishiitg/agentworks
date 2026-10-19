import { useCallback, useEffect, useRef, useState } from 'react'
import { agentApi } from '../../services/api'
import { useChatStore, waitForChatStoreHydration } from '../../stores/useChatStore'
import { useAppStore } from '../../stores/useAppStore'
import { useLLMStore } from '../../stores/useLLMStore'
import { hydrateTabEvents } from '../../utils/sessionRestore'
import { activateTab } from '../../utils/activateTab'
import { captureChatIdentity, isChatIdentityCurrent } from '../../utils/chatIdentity'

const PROFILE = 'caplayer'
const WORKSPACE = 'Chats/CapLayer'

/** Vault's existing durable chat, prepared only for an explicit assistant action. */
export function useVaultChat(enabled: boolean, selectedTabId?: string, navigationToken?: number) {
  const [tabId, setTabId] = useState<string | null>(null)
  const [error, setError] = useState('')
  const generation = useRef(0)
  const ready = useRef<string | null>(null)
  const pending = useRef<Promise<string | null> | null>(null)
  const prepare = useCallback((newConversation = false, force = false): Promise<string | null> => {
    if (!newConversation && !force && pending.current) return pending.current
    if (!newConversation && !force && ready.current && (!selectedTabId || ready.current === selectedTabId)) return Promise.resolve(ready.current)
    const request = ++generation.current
    const identity = captureChatIdentity()
    const current = () => request === generation.current && isChatIdentityCurrent(identity)
    setError('')
    const operation = (async () => {
      await waitForChatStoreHydration()
      if (!current()) return null
      if (selectedTabId && !newConversation) {
        const existing = useChatStore.getState().chatTabs[selectedTabId]
        if (existing?.metadata?.agentProfileId !== PROFILE || !existing.sessionId) throw new Error('This Vault chat is no longer available.')
        await hydrateTabEvents(existing.sessionId, { workspacePath: WORKSPACE, preferChatHistory: true, fallbackToChatHistory: true })
        if (!current()) return null
        ready.current = selectedTabId
        setTabId(selectedTabId)
        return selectedTabId
      }
      const conversation = newConversation
        ? await agentApi.startNewAgentProfileConversation(PROFILE, { conversation_key: 'main' })
        : await agentApi.resolveAgentProfileConversation(PROFILE, { conversation_key: 'main' }, null)
      if (!current()) return null
      const store = useChatStore.getState()
      if (newConversation) {
        for (const tab of Object.values(store.chatTabs)) {
          if (tab.metadata?.agentProfileId === PROFILE && tab.metadata.agentProfileConversationKey === 'main') store.setTabMetadata(tab.tabId, { isViewOnly: true })
        }
      }
      const created = await store.createChatTab('Chat', {
        mode: 'multi-agent', agentProfileId: PROFILE, agentProfileVersion: 1,
        agentProfileWorkspace: WORKSPACE, agentProfileProjectTitle: 'Vault',
        agentProfileChatContract: 'profile-v1', agentProfileConversationKey: conversation.conversation_key,
        agentProfileConversationId: conversation.conversation_id,
      }, conversation.session_id)
      if (!current()) return null
      store.setTabConfig(created, { selectedServers: ['NO_SERVERS'], selectedSkills: [] })
      await hydrateTabEvents(conversation.session_id, { workspacePath: WORKSPACE, preferChatHistory: true, fallbackToChatHistory: true })
      if (!current()) return null
      const app = useAppStore.getState()
      if (!app.adminPage && !app.showSchedulesOverview && !useLLMStore.getState().showLLMModal) activateTab(created)
      ready.current = created
      setTabId(created)
      return created
    })().catch(cause => {
      if (current()) setError(cause instanceof Error ? cause.message : 'Could not open Vault chat')
      return null
    }).finally(() => { if (request === generation.current) pending.current = null })
    pending.current = operation
    return operation
  }, [selectedTabId])
  useEffect(() => { if (enabled) void prepare(false, !!selectedTabId) }, [enabled, prepare, selectedTabId, navigationToken])
  useEffect(() => () => { generation.current += 1; ready.current = null; pending.current = null }, [])
  return { tabId, error, prepare, retry: () => void prepare(false, true), openNew: () => void prepare(true) }
}
