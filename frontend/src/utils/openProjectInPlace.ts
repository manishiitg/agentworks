import { openAppProduct } from './appNavigation'
import { getRawActiveWorkspaceId } from './desktopConnection'
import { openProductWorkspace } from './productWorkspaceNavigation'
import { parseProductRoute, type ProductRoute } from './productRoutes'
import { registerRemoteProject } from './remoteSessions'

/** Open a Crew wherever it lives without reloading the window (#3383). A project on another connection is noted
 * first, so its requests, chats and files go to its own server through this Mac's sign-in. Every place that opens a
 * project (the left menu, the quick switcher, Home, the Team monitor, notifications) uses this one. */
export function openProjectInPlace(connectionId: string, route: ProductRoute & { id: string }) {
  if (connectionId !== getRawActiveWorkspaceId()) registerRemoteProject(route.id, connectionId)
  window.dispatchEvent(new Event('close-app-sidebar'))
  openProductWorkspace('work')
  openAppProduct(route)
}

// A notification click for a Crew opens it here instead of reloading the window (desktop/lib/connections.js).
if (typeof window !== 'undefined') window.electronAPI?.onOpenProjectInPlace?.(({ connection, route }) => {
  const target = parseProductRoute(route)
  if (target?.surface === 'work' && target.id) openProjectInPlace(connection, { ...target, id: target.id })
})
