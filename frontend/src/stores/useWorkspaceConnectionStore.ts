import { getRawActiveWorkspaceId } from '../utils/desktopConnection'
import { create } from 'zustand'

export type WorkspaceConnectionType = 'local' | 'remote'
export interface WorkspaceProfile { id: string; name: string; type: WorkspaceConnectionType; apiBaseUrl: string }

export { getRawActiveWorkspaceId } from '../utils/desktopConnection'
export function getWorkspaceScopedStorageKey(key: string): string {
  const id = getRawActiveWorkspaceId()
  return id === 'local' ? key : `${key}:${id}`
}
const activeWorkspaceId = getRawActiveWorkspaceId()
const currentProfile: WorkspaceProfile = { id: activeWorkspaceId, name: activeWorkspaceId === 'local' ? 'Local' : 'Server', type: activeWorkspaceId === 'local' ? 'local' : 'remote', apiBaseUrl: '' }
export const useWorkspaceConnectionStore = create<{
  activeWorkspaceId: string
  profiles: WorkspaceProfile[]
  setProfiles: (profiles: WorkspaceProfile[]) => void
}>(set => ({
  activeWorkspaceId,
  profiles: [currentProfile],
  setProfiles: profiles => set({ profiles }),
}))
export function getActiveWorkspaceProfile(): WorkspaceProfile {
  return useWorkspaceConnectionStore.getState().profiles.find(p => p.id === activeWorkspaceId) || currentProfile
}
