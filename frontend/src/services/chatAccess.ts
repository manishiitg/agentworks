import { captureChatIdentity, isChatIdentityCurrent } from '../utils/chatIdentity'

export const chatAccessNotice = 'You no longer have access to this chat.'
let identity = -1
const denied = new Map<string, string>()
const listeners = new Set<() => void>()
function current() {
  const value = captureChatIdentity()
  if (value !== identity) { identity = value; denied.clear() }
  return value
}
export function getChatAccessNotice(session?: string | null): string | undefined {
  current()
  return session ? denied.get(session) : undefined
}
export function subscribeChatAccess(listener: () => void) { listeners.add(listener); return () => { listeners.delete(listener) } }
export function denyChatAccess(session: string, captured = captureChatIdentity()) {
  if (!isChatIdentityCurrent(captured)) return
  current()
  if (denied.has(session)) return
  denied.set(session, chatAccessNotice)
  console.info('[CHAT_ACCESS] event=refused source=server status=403 session=' + session)
  listeners.forEach(listener => listener())
}
// Only an explicit, successful server conversation resolution resets a refusal.
export function clearChatAccess(session: string, captured = captureChatIdentity()) {
  if (!isChatIdentityCurrent(captured)) return
  current()
  if (denied.delete(session)) listeners.forEach(listener => listener())
}
export async function readChat<T>(session: string, read: () => Promise<T>): Promise<T> {
  const captured = current()
  if (denied.has(session)) throw Object.assign(new Error(chatAccessNotice), { response: { status: 403 } })
  try { return await read() }
  catch (error) {
    if ((error as { response?: { status?: number } })?.response?.status === 403) denyChatAccess(session, captured)
    throw error
  }
}
