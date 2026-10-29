// Electron owns the selected server. A web link cannot change this identity.
export function getRawActiveWorkspaceId(): string {
  return typeof window === 'undefined' ? 'local' : window.electronAPI?.getDesktopConnection?.() || 'local'
}
