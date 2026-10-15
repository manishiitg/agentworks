import { create } from 'zustand'
import type { WorkspaceDestination, WorkspaceState } from '../services/workspaceState'
// Server state is deliberately memory-only and keyed by the authenticated
// account. Another person's browser-local workspace is never a fallback.
export const useWorkspaceReturnStore = create<{
  account?: string
  ready: boolean
  record: WorkspaceState | null
  restore: WorkspaceDestination | null
  homeOpen: boolean
  note: string
  navigationMode: 'fixed' | 'auto-hide'
  saveError: string
}>(() => ({ ready: false, record: null, restore: null, homeOpen: false, note: '', navigationMode: 'fixed', saveError: '' }))

export function openHome() {
  window.history.pushState({}, '', '/home')
  window.dispatchEvent(new PopStateEvent('popstate'))
}
