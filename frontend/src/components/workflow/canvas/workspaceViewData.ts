import { createContext, useContext } from 'react'
import type { UseWorkspaceStateReturn } from '../hooks/useWorkspaceState'

export interface WorkspaceViewData {
  workspace: UseWorkspaceStateReturn
  selectedRunFolder: string | null
  runFolderNames: string[]
}

export const WorkspaceViewDataContext = createContext<WorkspaceViewData | null>(null)

export function useWorkspaceViewData(): WorkspaceViewData {
  const data = useContext(WorkspaceViewDataContext)
  if (!data) throw new Error('useWorkspaceViewData must be used inside WorkspaceViewHost')
  return data
}
