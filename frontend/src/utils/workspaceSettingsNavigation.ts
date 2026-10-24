import { useAppStore } from '../stores/useAppStore'
import { useLLMStore } from '../stores/useLLMStore'
import { WORKSPACE_SETTINGS_PATHS, workspaceSettingsPage, type WorkspaceSettingsPage } from './workspaceSettingsRoutes'

export function syncWorkspaceSettings(page: WorkspaceSettingsPage | undefined): void {
  useLLMStore.getState().setShowLLMModal(page === 'providers')
  useAppStore.getState().setAdminPage(page && page !== 'providers' ? page : null)
}

/** Account settings are ordinary authenticated pages, independent of a project. */
export function openWorkspaceSettings(page: WorkspaceSettingsPage): void {
  const path = WORKSPACE_SETTINGS_PATHS[page]
  if (window.location.pathname !== path) window.history.pushState({ workspaceSettings: true }, '', path)
  syncWorkspaceSettings(page)
  window.dispatchEvent(new PopStateEvent('popstate'))
}

export function closeWorkspaceSettings(): void {
  if (!workspaceSettingsPage()) return
  if (window.history.state?.workspaceSettings) window.history.back()
  else {
    window.history.replaceState({}, '', '/home')
    window.dispatchEvent(new PopStateEvent('popstate'))
  }
}
