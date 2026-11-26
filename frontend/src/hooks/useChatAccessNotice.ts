import { useSyncExternalStore } from 'react'
import { getChatAccessNotice, subscribeChatAccess } from '../services/chatAccess'
export function useChatAccessNotice(session?: string | null) {
  return useSyncExternalStore(subscribeChatAccess, () => getChatAccessNotice(session), () => undefined)
}
