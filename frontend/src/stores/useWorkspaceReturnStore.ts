import { create } from 'zustand'
import type { WorkspaceDestination, WorkspaceState } from '../services/workspaceState'
// Server state is deliberately memory-only and keyed by the authenticated
// account. Another person's browser-local workspace is never a fallback.
export const useWorkspaceReturnStore = create<{
  account?: string
  workspace?: string
  ready: boolean
  record: WorkspaceState | null
  restore: WorkspaceDestination | null
  note: string
  navigationMode: 'fixed' | 'auto-hide'
  saveError: string
}>(() => ({ ready: false, record: null, restore: null, note: '', navigationMode: 'fixed', saveError: '' }))
