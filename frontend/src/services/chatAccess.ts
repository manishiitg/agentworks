import { remoteSessionConnection } from '../utils/remoteSessions'
import { captureChatIdentity, isChatIdentityCurrent } from '../utils/chatIdentity'

export const chatAccessNotice = 'You no longer have access to this chat.'
let identity = -1
const denied = new Map<string, string>()
const listeners = new Set<() => void>()
export function captureChatAccessScope(session: string) { return { identity: captureChatIdentity(), connection: remoteSessionConnection(session) } }
const key = (session: string) => JSON.stringify([session, remoteSessionConnection(session) || ''])
function current() {
  const value = captureChatIdentity()
  if (value !== identity) { identity = value; denied.clear() }
  return value
}
export function getChatAccessNotice(session?: string | null): string | undefined {
  current()
  return session ? denied.get(key(session)) : undefined
}
export function subscribeChatAccess(listener: () => void) { listeners.add(listener); return () => { listeners.delete(listener) } }
export function denyChatAccess(session: string, captured = captureChatAccessScope(session)) {
  if (!isChatIdentityCurrent(captured.identity) || remoteSessionConnection(session) !== captured.connection) return
  current()
  if (denied.has(key(session))) return
  denied.set(key(session), chatAccessNotice)
  console.info('[CHAT_ACCESS] event=refused source=server status=403 session=' + session)
  listeners.forEach(listener => listener())
}
// Only an explicit, successful server conversation resolution resets a refusal.
export function clearChatAccess(session: string, captured = captureChatIdentity()) {
  if (!isChatIdentityCurrent(captured)) return
  current()
  if (denied.delete(key(session))) listeners.forEach(listener => listener())
}
export async function readChat<T>(session: string, read: () => Promise<T>): Promise<T> {
  current()
  const captured = captureChatAccessScope(session)
  if (denied.has(key(session))) throw Object.assign(new Error(chatAccessNotice), { response: { status: 403 } })
  try { return await read() }
  catch (error) {
    if ((error as { response?: { status?: number } })?.response?.status === 403) denyChatAccess(session, captured)
    throw error
  }
}
