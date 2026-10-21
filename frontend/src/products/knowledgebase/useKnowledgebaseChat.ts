import { useEffect, useRef, useState } from 'react'
import type { ChatAreaRef } from '../../components/ChatArea'
import { agentApi } from '../../services/api'
import { knowledgebaseError, type KnowledgeBootstrap } from '../../services/knowledgebaseApi'
import { useChatStore, waitForChatStoreHydration } from '../../stores/useChatStore'
import { hydrateTabEvents } from '../../utils/sessionRestore'
import { captureChatIdentity, isChatIdentityCurrent } from '../../utils/chatIdentity'

/** Retained Brain history is read-only; this path never creates a server conversation. */
export function useKnowledgebaseChat(bootstrap: KnowledgeBootstrap | null, scope: string, legacyView: boolean, selectedTabId?: string, navigationToken?: number) {
  const chatRef = useRef<ChatAreaRef>(null)
  const [requested, setRequested] = useState<string | null>(null)
  const [attempt, setAttempt] = useState(0)
  const [state, setState] = useState<{ scope: string; tabId: string | null; error: string; busy: boolean }>({ scope, tabId: null, error: '', busy: false })
  const canReadHistory = !!bootstrap && bootstrap.can_write !== false
  const enabled = canReadHistory && (requested === scope || legacyView)
  const workspace = bootstrap?.chat_workspace
  useEffect(() => {
    if (!enabled || !workspace) return
    let cancelled = false
    const identity = captureChatIdentity()
    const current = () => !cancelled && isChatIdentityCurrent(identity)
    setState({ scope, tabId: null, error: '', busy: true })
    void (async () => {
      await waitForChatStoreHydration()
      if (!current()) return
      const store = useChatStore.getState()
      if (selectedTabId) {
        const existing = store.chatTabs[selectedTabId]
        if (existing?.metadata?.agentProfileId !== 'knowledgebase' || !existing.sessionId) throw new Error('This Brain conversation is no longer available.')
        await hydrateTabEvents(existing.sessionId, { workspacePath: workspace, preferChatHistory: true, fallbackToChatHistory: true })
        if (current()) { store.setTabMetadata(selectedTabId, { isViewOnly: true }); setState({ scope, tabId: selectedTabId, error: '', busy: false }) }
        return
      }
      const { sessions } = await agentApi.listChatHistorySessions(1, 0, workspace)
      if (!current()) return
      const previous = sessions[0]
      if (!previous) throw new Error('No previous Brain conversations are available.')
      const id = await store.createChatTab(previous.title || 'Previous Brain conversation', {
        mode: 'multi-agent', agentProfileId: 'knowledgebase', agentProfileVersion: 1,
        agentProfileWorkspace: workspace, agentProfileProjectTitle: 'Brain', isViewOnly: true,
      }, previous.session_id)
      if (!current()) return
      await hydrateTabEvents(previous.session_id, { workspacePath: workspace, preferChatHistory: true, fallbackToChatHistory: true })
      if (current()) setState({ scope, tabId: id, error: '', busy: false })
    })().catch(cause => { if (current()) setState({ scope, tabId: null, error: knowledgebaseError(cause), busy: false }) })
    return () => { cancelled = true }
  }, [enabled, workspace, scope, attempt, selectedTabId, navigationToken])
  return {
    chatRef,
    tabId: state.scope === scope ? state.tabId : null,
    error: state.scope === scope ? state.error : '',
    busy: state.scope === scope && state.busy,
    request: () => { if (canReadHistory) setRequested(scope) },
    retry: () => setAttempt(value => value + 1),
  }
}
