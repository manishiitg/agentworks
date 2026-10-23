import { useState } from 'react'
import { useChatStore } from '../../stores/useChatStore'
import { openWorkspacePaneChatWithDraft } from '../../utils/workspacePaneChat'
import { isProjectVaultChat, revealProjectVaultChat } from '../../utils/projectVaultNavigation'

/** Setup always belongs to the displayed project's exact tab. */
export function useVaultSetupDraft(tabId?: string) {
  const [error, setError] = useState('')
  const open = async (message: string) => {
    setError('')
    try {
      const state = useChatStore.getState()
      const tab = state.chatTabs[tabId ?? state.activeTabId ?? '']
      if (!isProjectVaultChat(tab, true)) throw new Error('Open your own Crew or Goal chat to manage Vault here.')
      revealProjectVaultChat(tab)
      await openWorkspacePaneChatWithDraft({ tabId: tab.tabId, message })
    } catch (cause) { setError(cause instanceof Error ? cause.message : 'Could not open this project chat.') }
  }
  return { open, error }
}
