import { useChatStore } from '../stores/useChatStore'
import { useConnectedWorkStore } from '../stores/useConnectedWorkStore'

/** Opens a page of a connected server on its website in the browser (the Servers page's Open website). */
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

