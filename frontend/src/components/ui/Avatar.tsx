import { useEffect, useState } from 'react'
import { Bot, Eye, Leaf, Rocket, Star, UserRound } from 'lucide-react'
import api, { getAuthToken, getDesktopApiBaseUrl, getLocalApiBaseUrl } from '../../services/api'
import { useAuthStore } from '../../stores/useAuthStore'
import { getRawActiveWorkspaceId, useWorkspaceConnectionStore } from '../../stores/useWorkspaceConnectionStore'

export type AvatarFields = { avatar_url?: string; avatar_icon?: string; avatar_color?: string }
const icons = { person: UserRound, rocket: Rocket, star: Star, leaf: Leaf, bot: Bot, eye: Eye }
const colours: Record<string, string> = { blue: 'bg-blue-100 text-blue-800 dark:bg-blue-950 dark:text-blue-200', purple: 'bg-purple-100 text-purple-800 dark:bg-purple-950 dark:text-purple-200', green: 'bg-green-100 text-green-800 dark:bg-green-950 dark:text-green-200', orange: 'bg-orange-100 text-orange-800 dark:bg-orange-950 dark:text-orange-200' }

// Never put credentials in an image URL, or send them to a model-supplied URL.
// A connected Team uses its own saved network, not the currently selected one.
export function Avatar({ name, avatar_url, avatar_icon, avatar_color, connection, className = 'h-6 w-6 text-xs' }: AvatarFields & { name: string; connection?: string; className?: string }) {
  const actor = useAuthStore(state => state.user?.id)
  const workspace = useWorkspaceConnectionStore(state => state.activeWorkspaceId)
  const scope = JSON.stringify([actor, workspace, connection, avatar_url])
  const [photo, setPhoto] = useState<{ scope: string; url: string }>()
  useEffect(() => {
    let live = true, objectURL = ''
    const controller = new AbortController()
    // The server emits only this relative route. Refuse schemes, foreign hosts,
    // extra path segments and control characters before using auth transport.
    if (avatar_url && /^\/api\/account\/avatars\/[a-zA-Z0-9_%.-]+\?[^\s#]*$/.test(avatar_url)) {
      const read = async () => {
        let blob: Blob
        if (!connection || connection === getRawActiveWorkspaceId()) blob = (await api.get<Blob>(avatar_url, { responseType: 'blob', skipSessionContext: true, signal: controller.signal })).data
        else {
          const route = connection === 'local' ? `${getLocalApiBaseUrl()}${avatar_url}` : `${getDesktopApiBaseUrl()}/api/connections/${encodeURIComponent(connection)}${avatar_url}`
          const response = await fetch(route, { headers: { Authorization: `Bearer ${getAuthToken() || ''}` }, signal: controller.signal })
          if (!response.ok) return
          blob = await response.blob()
        }
        if (!live || blob.type !== 'image/jpeg' || blob.size > 65536) return
        objectURL = URL.createObjectURL(blob)
        setPhoto({ scope, url: objectURL })
      }
      void read().catch(() => undefined)
    }
    return () => { live = false; controller.abort(); if (objectURL) URL.revokeObjectURL(objectURL) }
  }, [scope, avatar_url, connection])
  const Icon = avatar_icon && Object.hasOwn(icons, avatar_icon) ? icons[avatar_icon as keyof typeof icons] : undefined
  return <span aria-hidden="true" className={`inline-flex shrink-0 items-center justify-center overflow-hidden rounded-full font-medium ${avatar_color && Object.hasOwn(colours, avatar_color) ? colours[avatar_color] : 'bg-muted text-muted-foreground'} ${className}`}>
    {photo?.scope === scope ? <img src={photo.url} alt="" className="m-0 block h-full w-full object-cover" onError={() => setPhoto(undefined)} /> : Icon ? <Icon className="h-[60%] w-[60%]" /> : name.trim().slice(0, 1).toUpperCase() || <UserRound className="h-[60%] w-[60%]" />}
  </span>
}
