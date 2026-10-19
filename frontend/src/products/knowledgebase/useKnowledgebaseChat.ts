import { useEffect, useRef, useState } from 'react'
import type { ChatAreaRef } from '../../components/ChatArea'
import { agentApi } from '../../services/api'
import type { KnowledgeBootstrap } from '../../services/knowledgebaseApi'
import { knowledgebaseError } from '../../services/knowledgebaseApi'
import { useChatStore, waitForChatStoreHydration } from '../../stores/useChatStore'
import { hydrateTabEvents } from '../../utils/sessionRestore'
import { activateTab } from '../../utils/activateTab'
import { captureChatIdentity, isChatIdentityCurrent } from '../../utils/chatIdentity'

type PendingAsk = { message: string; identity: ReturnType<typeof captureChatIdentity>; scope: string; resolve: () => void; reject: (error: Error) => void }

/** Brain's existing conversation is resolved only for an explicit chat or
 * model-settings request. Closing the secondary pane keeps its draft alive. */
export function useKnowledgebaseChat(bootstrap: KnowledgeBootstrap | null, scope: string, models: boolean, selectedTabId?: string, navigationToken?: number) {
  const chatRef = useRef<ChatAreaRef>(null)
  const [requested, setRequested] = useState<string | null>(null)
  const [state, setState] = useState<{ scope: string; tabId: string | null; error: string; busy: boolean }>({ scope, tabId: null, error: '', busy: false })
  const [attempt, setAttempt] = useState(0)
  const [queueRevision, setQueueRevision] = useState(0)
  const pending = useRef<PendingAsk[]>([])
  const delivery = useRef<{ scope: string; tabId: string | null; canChat: boolean } | null>(null)
  const rotate = useRef(false)
  const canChat = !!bootstrap && bootstrap.can_write !== false
  const enabled = canChat && (requested === scope || models)
  const workspace = bootstrap?.chat_workspace
  const tabId = state.scope === scope ? state.tabId : null
  delivery.current = { scope, tabId, canChat }
  useEffect(() => () => { delivery.current = null }, [])
  useEffect(() => {
    return () => {
      pending.current.splice(0).forEach(job => job.reject(new Error('The Brain account or server changed.')))
    }
  }, [scope])
  useEffect(() => {
    if (!enabled || !workspace) return
    let cancelled = false
    const identity = captureChatIdentity()
    const current = () => !cancelled && isChatIdentityCurrent(identity)
    const startNew = rotate.current
    rotate.current = false
    setState({ scope, tabId: null, error: '', busy: true })
    void (async () => {
      await waitForChatStoreHydration()
      if (!current()) return
      const store = useChatStore.getState()
      if (selectedTabId && !startNew) {
        const existing = store.chatTabs[selectedTabId]
        if (existing?.metadata?.agentProfileId !== 'knowledgebase' || !existing.sessionId) throw new Error('This Brain chat is no longer available.')
        await hydrateTabEvents(existing.sessionId, { workspacePath: workspace, preferChatHistory: true, fallbackToChatHistory: true })
        if (current()) setState({ scope, tabId: selectedTabId, error: '', busy: false })
        return
      }
      const conversation = await (startNew ? agentApi.startNewAgentProfileConversation('knowledgebase', { conversation_key: 'main' }) : agentApi.resolveAgentProfileConversation('knowledgebase', { conversation_key: 'main' }))
      if (!current()) return
      if (startNew) {
        for (const tab of Object.values(store.chatTabs)) {
          if (tab.metadata?.agentProfileId === 'knowledgebase' && tab.metadata.agentProfileConversationKey === 'main') store.setTabMetadata(tab.tabId, { isViewOnly: true })
        }
      }
      const id = await store.createChatTab('Access chat', {
        mode: 'multi-agent', agentProfileId: 'knowledgebase', agentProfileVersion: 1,
        agentProfileWorkspace: workspace, agentProfileProjectTitle: 'Brain',
        agentProfileChatContract: 'profile-v1', agentProfileConversationKey: conversation.conversation_key,
        agentProfileConversationId: conversation.conversation_id, agentProfileMCPSelectionInitialized: true,
      }, conversation.session_id)
      if (!current()) return
      store.setTabConfig(id, { selectedServers: ['NO_SERVERS'], selectedSkills: [] })
      if ((store.tabEvents[conversation.session_id]?.length ?? 0) === 0) await hydrateTabEvents(conversation.session_id, { workspacePath: workspace, preferChatHistory: true, fallbackToChatHistory: true })
      if (current()) setState({ scope, tabId: id, error: '', busy: false })
    })().catch(error => {
      if (!current()) return
      setState({ scope, tabId: null, error: knowledgebaseError(error), busy: false })
      pending.current.splice(0).forEach(job => job.reject(new Error(knowledgebaseError(error))))
    })
    return () => { cancelled = true }
  }, [enabled, workspace, scope, attempt, selectedTabId, navigationToken])
  useEffect(() => {
    if (!canChat) {
      setRequested(null)
      pending.current.splice(0).forEach(job => job.reject(new Error('Brain chat is unavailable.')))
    }
  }, [canChat])
  useEffect(() => {
    if (!tabId || !chatRef.current || !canChat) return
    const jobs = pending.current.splice(0)
    const chat = chatRef.current
    void (async () => {
      for (const job of jobs) {
        if (delivery.current?.scope !== job.scope || delivery.current.tabId !== tabId || !delivery.current.canChat || !isChatIdentityCurrent(job.identity)) { job.reject(new Error('The Brain account or server changed.')); continue }
        try { activateTab(tabId); await chat.submitQuery(job.message); job.resolve() }
        catch (error) { job.reject(error instanceof Error ? error : new Error('Could not send to Brain chat.')) }
      }
    })()
  }, [tabId, queueRevision, scope, canChat])
  return {
    chatRef, tabId,
    error: state.scope === scope ? state.error : '',
    busy: state.scope === scope && state.busy,
    request: () => { if (canChat) setRequested(scope) },
    ask: (message: string) => new Promise<void>((resolve, reject) => {
      if (!canChat) { reject(new Error('Brain chat is unavailable.')); return }
      pending.current.push({ message, identity: captureChatIdentity(), scope, resolve, reject })
      setRequested(scope); setQueueRevision(value => value + 1)
    }),
    retry: () => setAttempt(value => value + 1),
    newConversation: () => { if (canChat) { rotate.current = true; setRequested(scope); setAttempt(value => value + 1) } },
  }
}
