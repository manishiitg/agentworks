import { create } from 'zustand'
import { captureChatIdentity, isChatIdentityCurrent } from '../../utils/chatIdentity'
import { getRawActiveWorkspaceId } from '../../utils/desktopConnection'
import { currentProductRoute } from '../../utils/productRoutes'

export type SidebarProjectAction = 'new' | 'chat' | 'skills'
type Request = {
  projectId: string
  action: SidebarProjectAction
  identity: number
  connection: string
}

export const useSidebarProjectAction = create<{ request: Request | null }>(() => ({ request: null }))
let stopWatching: (() => void) | undefined

function stillOpening(request: Request): boolean {
  const route = currentProductRoute()
  return isChatIdentityCurrent(request.identity) && getRawActiveWorkspaceId() === request.connection &&
    route?.surface === 'work' && route.id === request.projectId
}

/** One in-memory navigation handoff, not a saved action or a permission grant. */
export function requestSidebarProjectAction(projectId: string, action: SidebarProjectAction, open: () => void) {
  const previous = useSidebarProjectAction.getState().request
  if (previous) takeSidebarProjectAction(previous)
  const request = { projectId, action, identity: captureChatIdentity(), connection: getRawActiveWorkspaceId() }
  open()
  if (!stillOpening(request)) return
  const movedAway = () => { if (!stillOpening(request)) takeSidebarProjectAction(request) }
  window.addEventListener('popstate', movedAway)
  window.addEventListener('product-navigation-changed', movedAway)
  stopWatching = () => {
    window.removeEventListener('popstate', movedAway)
    window.removeEventListener('product-navigation-changed', movedAway)
  }
  useSidebarProjectAction.setState({ request })
}

export function takeSidebarProjectAction(request: Request): boolean {
  if (useSidebarProjectAction.getState().request !== request) return false
  stopWatching?.()
  stopWatching = undefined
  useSidebarProjectAction.setState({ request: null })
  return stillOpening(request)
}
