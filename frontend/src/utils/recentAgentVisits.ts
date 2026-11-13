import type { ChatTab } from '../stores/useChatStore'

/** Personal sidebar only: never promote a teammate's opened chat into Recents. */
export function isOwnRecentAgent(meta: ChatTab['metadata'], user?: string): boolean {
  if (!user || meta?.agentProfileId !== 'work' || meta.isScheduledRun || meta.isBotRun) return false
  if (meta.agentProfileTabOwnerID) return meta.agentProfileTabOwnerID === user
  return meta.agentProfileTabCanWrite !== false && !meta.isViewOnly
}
