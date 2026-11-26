import { Avatar, type AvatarFields } from './ui/Avatar'
import { useEffect, useState } from 'react'
import api from '../services/api'

/** One presentation on Mac and web. The inbox is an address, never a name or
 * permission: the server admits the reader and resolves the current identity. */
// fallback: the sender's name the server stamped on the message when it was delivered. It shows when this computer
// can't look the conversation up (a teammate's chat watched from another computer), instead of "Person · Agent".
export function AgentMessageIdentity({ inboxID, messageID, sent = false, sessionID, fallback }: { inboxID?: string; messageID?: string; sent?: boolean; sessionID?: string; fallback?: string }) {
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
    <Avatar name={identity?.label || fallback || ''} {...identity} className="h-5 w-5 text-[10px]" />
    <span className="min-w-0 truncate">{sent ? 'You → ' : ''}{identity?.label || fallback?.trim() || 'Person · Agent'}</span>
  </span>
}
