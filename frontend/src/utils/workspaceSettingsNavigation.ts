import { useAppStore } from '../stores/useAppStore'
import { useLLMStore } from '../stores/useLLMStore'
import { productUrlViewChanged } from './productUrlNavigation'

/** Open one existing settings page, leaving Home when it covers the workspace. */
export function openWorkspaceSettings(page: 'users' | 'mcp' | 'providers'): void {
  useLLMStore.getState().setShowLLMModal(page === 'providers')
  useAppStore.getState().setAdminPage(page === 'providers' ? null : page)
  if (window.location.pathname === '/home') productUrlViewChanged()
}
