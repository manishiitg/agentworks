import { useChatStore } from '../stores/useChatStore'
import { useConnectedWorkStore } from '../stores/useConnectedWorkStore'

/** The Mac app lists only this Mac's projects (owner, 24 Nov, #3779): a server project, its chats and its Team open on
 * that server's website in the browser. A server project reaches the Mac only as an online project in a Mac Team. */
export async function openOnServerWebsite(connectionId: string, route = '/') {
  const find = () => useConnectedWorkStore.getState().connections.find(connection => connection.id === connectionId)?.server
  // A notification can arrive before this window has read its connections.
  const server = find() || await useConnectedWorkStore.getState().refresh().then(find, () => undefined)
  let url = ''
  try { url = server ? new URL(route, server).toString() : '' } catch { url = '' }
  if (!url) { useChatStore.getState().addToast('Could not find this server. Open it from its website.', 'error'); return }
  if (window.electronAPI?.openExternal) window.electronAPI.openExternal(url)
  else window.open(url, '_blank', 'noopener,noreferrer')
}

// A notification click for work on a server opens it on that server's website (desktop/lib/connections.js).
if (typeof window !== 'undefined') window.electronAPI?.onOpenOnServerWebsite?.(({ connection, route }) => void openOnServerWebsite(connection, route))
