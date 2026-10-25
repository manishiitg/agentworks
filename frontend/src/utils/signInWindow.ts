/** Reserve a browser tab in the click gesture; Electron opens its system browser by IPC. */
export function prepareSignInWindow() {
  const external = window.electronAPI?.openExternal
  let popup: Window | null = null
  if (!external) {
    popup = window.open('about:blank', '_blank')
    if (popup) popup.opener = null
  }
  return {
    open(url: string): boolean {
      if (external) { external(url); return true }
      if (!popup || popup.closed) return false
      try { popup.location.href = url; return true } catch { popup.close(); return false }
    },
    close() { if (popup && !popup.closed) popup.close() },
  }
}
