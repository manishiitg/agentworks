import { useEffect, useState } from 'react'
import api, { getAuthToken, getDesktopApiBaseUrl } from '../../services/api'
import { Avatar, type AvatarFields } from './Avatar'

// Saved connection.user is the sign-in snapshot, not the current profile.
// Read the account named by this footer via its own existing connection.
export function AccountAvatar({ name, accountID, connection, className }: { name: string; accountID: string; connection?: string; className?: string }) {
  const scope = JSON.stringify([accountID, connection])
  const [profile, setProfile] = useState<{ scope: string; fields: AvatarFields }>()
  useEffect(() => {
    let live = true, sequence = 0
    const controller = new AbortController()
    const read = async () => {
      const request = ++sequence
      try {
        let fields: AvatarFields
        if (connection) {
          const response = await fetch(`${getDesktopApiBaseUrl()}/api/connections/${encodeURIComponent(connection)}/api/account/avatar`, {
            headers: { Authorization: `Bearer ${getAuthToken() || ''}` }, signal: controller.signal,
          })
          if (!response.ok) return
          fields = await response.json() as AvatarFields
        } else fields = (await api.get<AvatarFields>('/api/account/avatar', { skipSessionContext: true, signal: controller.signal })).data
        if (live && request === sequence) setProfile({ scope, fields })
      } catch { /* The initial remains when the account cannot be reached. */ }
    }
    void read()
    window.addEventListener('focus', read)
    window.addEventListener('agentworks:profile-updated', read)
    return () => { live = false; controller.abort(); window.removeEventListener('focus', read); window.removeEventListener('agentworks:profile-updated', read) }
  }, [scope, connection])
  return <Avatar name={name} {...(profile?.scope === scope ? profile.fields : {})} connection={connection} className={className} />
}
