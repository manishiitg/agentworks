import { Avatar, type AvatarFields } from './ui/Avatar'
import { useEffect, useState } from 'react'
import api from '../services/api'

/** One presentation on Mac and web. The inbox is an address, never a name or
 * permission: the server admits the reader and resolves the current identity. */
export function AgentMessageIdentity({ inboxID, messageID, sent = false, sessionID }: { inboxID?: string; messageID?: string; sent?: boolean; sessionID?: string }) {
  const [identity, setIdentity] = useState<AvatarFields & { label: string; address?: string }>()
  useEffect(() => {
    let active = true
    let sequence = 0
    const abort = new AbortController()
    setIdentity(undefined)
    const read = () => {
      if (!inboxID) return
      const request = ++sequence
      void api.get<AvatarFields & { label: string; address?: string }>('/api/agent-message-identity', {
        params: { inbox_id: inboxID, message_id: messageID || '', direction: sent ? 'sent' : 'incoming' },
        ...(sessionID ? { headers: { 'X-Session-ID': sessionID } } : {}), signal: abort.signal,
      }).then(({ data }) => { if (active && request === sequence) setIdentity(data) }).catch(() => { /* Never replace a missing name with a routing ID. */ })
    }
    read()
    window.addEventListener('focus', read)
    return () => { active = false; abort.abort(); window.removeEventListener('focus', read) }
  }, [inboxID, messageID, sent, sessionID])
  return <span className="inline-flex min-w-0 max-w-full items-center gap-2 align-middle" title={identity?.address || inboxID || undefined}>
    <Avatar name={identity?.label || ''} {...identity} className="h-5 w-5 text-[10px]" />
    <span className="min-w-0 truncate">{sent ? 'You → ' : ''}{identity?.label || 'Person · Agent'}</span>
  </span>
}
