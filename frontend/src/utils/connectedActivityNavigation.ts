import { getRawActiveWorkspaceId } from './desktopConnection'
import { captureChatIdentity, isChatIdentityCurrent } from './chatIdentity'
import { useChatStore } from '../stores/useChatStore'
import { openGlobalActivitySession, openGlobalTab } from './globalProductNavigation'

const pendingKey = 'agentworks-open-connected-activity'

export async function openConnectedActivity(connection: string, sessionId: string): Promise<void> {
  if (!window.electronAPI?.openDesktopConnection) return
  // Only a selector crosses the reload. The destination re-reads its own
  // authorized activity; no foreign session object or credentials are copied.
  sessionStorage.setItem(pendingKey, JSON.stringify({ connection, sessionId }))
  try { await window.electronAPI.openDesktopConnection(connection, '/home') }
  catch {
    sessionStorage.removeItem(pendingKey)
    useChatStore.getState().addToast('Could not open this server. Try again.', 'error')
  }
}

export async function restoreConnectedActivity(): Promise<void> {
  const raw = sessionStorage.getItem(pendingKey)
  if (!raw) return
  let pending: { connection: string; sessionId: string }
  try { pending = JSON.parse(raw) } catch { sessionStorage.removeItem(pendingKey); return }
  if (pending.connection !== getRawActiveWorkspaceId()) return
  sessionStorage.removeItem(pendingKey)
  const identity = captureChatIdentity()
  try {
    const sessions = await useChatStore.getState().getActiveSessions(true)
    if (!isChatIdentityCurrent(identity)) return
    const session = sessions.find(item => item.session_id === pending.sessionId)
    if (!session) throw new Error('Activity is no longer available on this server.')
    await openGlobalActivitySession(session, { source: 'connected-monitor' })
    if (!isChatIdentityCurrent(identity)) return
    // Crew navigation restores its server-listed main/side tabs. Select the
    // exact session after that list arrives rather than fabricating a binding.
    await new Promise<void>((resolve, reject) => {
      let unsubscribe = () => {}
      const timer = window.setTimeout(() => { unsubscribe(); reject(new Error('Could not open this conversation. Try the activity again.')) }, 15_000)
      const check = () => {
        if (!isChatIdentityCurrent(identity)) { clearTimeout(timer); unsubscribe(); resolve(); return }
        const tab = Object.values(useChatStore.getState().chatTabs).find(item => item.sessionId === pending.sessionId)
        if (!tab) return
        clearTimeout(timer); unsubscribe(); openGlobalTab(tab.tabId); resolve()
      }
      unsubscribe = useChatStore.subscribe(check)
      check()
    })
  } catch (cause) {
    if (isChatIdentityCurrent(identity)) useChatStore.getState().addToast(cause instanceof Error ? cause.message : 'Could not open this activity. Try again.', 'error')
  }
}
