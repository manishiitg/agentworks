import { useChatStore, waitForChatStoreHydration, type ChatTab } from '../stores/useChatStore'
import { useGlobalPresetStore } from '../stores/useGlobalPresetStore'
import { useWorkflowStore } from '../stores/useWorkflowStore'
import { activateTab } from './activateTab'
import { openProductWorkspace } from './productWorkspaceNavigation'
import { writeWorkspaceViewPreference } from './workspaceViewPreference'
import { setWorkspaceViewTarget } from '../hooks/useWorkspaceViewTarget'

export function isProjectVaultChat(tab: ChatTab | undefined, includeRelay = false): tab is ChatTab {
  const meta = tab?.metadata
  if (!tab || !meta || meta.isViewOnly || meta.workshopMode === 'run' || meta.isExecutionRun || meta.isScheduledRun || meta.isBotRun || meta.agentProfileTabCanWrite === false) return false
  if (meta.agentProfileId === 'work' && meta.agentProfileProjectId && !meta.agentProfileBuilder) return true
  if (meta.mode !== 'workflow' || meta.phaseId !== 'workflow-builder' || !meta.presetQueryId) return false
  return useGlobalPresetStore.getState().workflowPresets.some(preset => preset.id === meta.presetQueryId && (includeRelay || preset.workflowKind !== 'relay'))
}

/** Reveal the actual product as well as its tab, so a draft cannot stay behind
 * another product or an account-settings overlay. Never creates a Vault chat. */
export function revealProjectVaultChat(tab: ChatTab, openVault = false) {
  if (!isProjectVaultChat(tab, true)) throw new Error('Open your own Crew or Goal tab to manage Vault.')
  const crew = tab.metadata?.agentProfileId === 'work'
  if (crew && openVault) writeWorkspaceViewPreference('work', tab.metadata!.agentProfileProjectId!, 'mcp')
  openProductWorkspace(crew ? 'work' : useGlobalPresetStore.getState().workflowPresets.find(preset => preset.id === tab.metadata?.presetQueryId)?.workflowKind === 'relay' ? 'relays' : 'agentworks')
  if (!activateTab(tab.tabId)) throw new Error('This project chat is no longer available.')
  if (openVault) {
    if (crew) {
      setWorkspaceViewTarget('mcp', 'vault')
      window.dispatchEvent(new CustomEvent('agentworks:open-project-vault', { detail: { tabId: tab.tabId } }))
    } else useWorkflowStore.getState().openWorkspaceView('mcp', 'vault')
  }
}

/** My Vault is navigation to the person's last real project chat. */
export async function openLastProjectVaultChat(): Promise<string> {
  await waitForChatStoreHydration()
  const state = useChatStore.getState()
  if (!useGlobalPresetStore.getState().workflowPresetsLoaded && Object.values(state.chatTabs).some(tab => tab.metadata?.phaseId === 'workflow-builder')) await useGlobalPresetStore.getState().refreshPresets()
  const candidates = Object.values(state.chatTabs).filter(tab => isProjectVaultChat(tab))
  const target = candidates.find(tab => tab.tabId === state.activeTabId)
    ?? candidates.sort((a, b) => (b.lastAccessedAt ?? b.createdAt) - (a.lastAccessedAt ?? a.createdAt))[0]
  if (!target) throw new Error('Open a Crew or Goal chat first, then choose My vaults. Setup happens in that chat.')
  revealProjectVaultChat(target, true)
  return target.tabId
}

export function openMyVaults() {
  void openLastProjectVaultChat().catch(error => useChatStore.getState().addToast(error instanceof Error ? error.message : 'Open a Crew or Goal chat to manage Vault.', 'error'))
}
