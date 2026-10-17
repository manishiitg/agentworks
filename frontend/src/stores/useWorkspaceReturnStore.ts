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
  firstVisit: boolean
  note: string
  navigationMode: 'fixed' | 'auto-hide'
  saveError: string
}>(() => ({ ready: false, record: null, restore: null, firstVisit: false, note: '', navigationMode: 'fixed', saveError: '' }))
