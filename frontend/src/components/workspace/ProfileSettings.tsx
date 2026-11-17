import { useEffect, useRef, useState } from 'react'
import api, { getAuthToken, getDesktopApiBaseUrl } from '../../services/api'
import { useAuthStore } from '../../stores/useAuthStore'
import { Avatar, type AvatarFields } from '../ui/Avatar'

/** The caller's own picture on this app's server, or, with `connection`, on the server that hosts a Team (its
 * account is the one teammates see). Same route either way, through the saved-connection proxy. */
async function avatarRequest(connection: string | undefined, method: 'GET' | 'PUT', body?: unknown): Promise<AvatarFields> {
  if (!connection) return (method === 'GET' ? await api.get<AvatarFields>('/api/account/avatar', { skipSessionContext: true }) : await api.put<AvatarFields>('/api/account/avatar', body, { skipSessionContext: true })).data
  const response = await fetch(`${getDesktopApiBaseUrl()}/api/connections/${encodeURIComponent(connection)}/api/account/avatar`, { method, headers: { Authorization: `Bearer ${getAuthToken() || ''}`, 'Content-Type': 'application/json' }, ...(body === undefined ? {} : { body: JSON.stringify(body) }) })
  if (!response.ok) throw Object.assign(new Error('save failed'), { response: { data: await response.text().catch(() => '') } })
  return await response.json() as AvatarFields
}

/** Personal account only. The server revalidates every photo, crop and write. */
export default function ProfileSettings({ connection }: { connection?: string } = {}) {
  const user = useAuthStore(state => state.user)
  const [saved, setSaved] = useState<AvatarFields>({})
  const [icon, setIcon] = useState('person'), [colour, setColour] = useState('blue')
  const [photo, setPhoto] = useState<{ bytes: string; url: string }>()
  const [zoom, setZoom] = useState(1), [x, setX] = useState(.5), [y, setY] = useState(.5)
  const [busy, setBusy] = useState(false), [error, setError] = useState(''), [notice, setNotice] = useState('')
  const [loaded, setLoaded] = useState(false)
  const live = useRef(true), read = useRef(0)
  const canvas = useRef<HTMLCanvasElement>(null)
  useEffect(() => {
    live.current = true
    void avatarRequest(connection, 'GET').then(data => {
      if (!live.current) return
      setSaved(data); setIcon(data.avatar_icon || 'person'); setColour(data.avatar_color || 'blue'); setLoaded(true)
    }).catch(() => { if (live.current) setError('Your current picture could not be loaded. Reopen Profile to try again.') })
    return () => { live.current = false; read.current++ }
  }, [connection])
  useEffect(() => {
    if (!photo) return
    const image = new Image(); let active = true
    image.onload = () => {
      const ctx = canvas.current?.getContext('2d'); if (!ctx || !active) return
      const side = Math.min(image.width, image.height) / zoom
      ctx.fillStyle = '#fff'; ctx.fillRect(0, 0, 256, 256)
      ctx.drawImage(image, (image.width - side) * x, (image.height - side) * y, side, side, 0, 0, 256, 256)
    }
    image.src = photo.url
    return () => { active = false }
  }, [photo, zoom, x, y])
  const choosePhoto = async (file?: File) => {
    if (!file) return
    setError(''); setNotice('')
    if (!['image/jpeg', 'image/png', 'image/webp'].includes(file.type) || file.size > 5 * 1024 * 1024) { setError('Choose a PNG, JPEG or WebP photo smaller than 5 MB.'); return }
    const version = ++read.current
    const reader = new FileReader()
    reader.onload = () => {
      if (!live.current || version !== read.current) return
      const url = String(reader.result)
      const image = new Image()
      image.onload = () => {
        if (!live.current || version !== read.current) return
        if (image.width > 8192 || image.height > 8192 || image.width * image.height > 16000000) { setError('Choose a photo up to 16 megapixels and 8192 pixels per side.'); return }
        setPhoto({ bytes: url.slice(url.indexOf(',') + 1), url }); setZoom(1); setX(.5); setY(.5)
      }
      image.onerror = () => { if (live.current) setError('This photo could not be opened.') }
      image.src = url
    }
    reader.onerror = () => setError('This photo could not be read.')
    reader.readAsDataURL(file)
  }
  const save = async (remove = false) => {
    setBusy(true); setError(''); setNotice('')
    try {
      const data = await avatarRequest(connection, 'PUT', remove ? { operation: 'remove' } : photo ? { operation: 'set', image_base64: photo.bytes, crop: { zoom, x, y } } : { operation: 'set', icon, color: colour })
      if (!live.current) return
      setSaved(data); setPhoto(undefined); setNotice(remove ? 'Picture removed.' : 'Profile picture saved.')
      if (!connection) useAuthStore.setState(state => ({ user: state.user ? { ...state.user, avatar_url: '', avatar_icon: '', avatar_color: '', ...data } : null }))
      window.dispatchEvent(new CustomEvent('agentworks:profile-updated'))
      window.dispatchEvent(new CustomEvent('agentworks:team-updated'))
    } catch (cause) {
      if (live.current) setError(typeof (cause as { response?: { data?: unknown } }).response?.data === 'string' ? String((cause as { response: { data: string } }).response.data) : 'Your picture could not be saved. Try again.')
    } finally { if (live.current) setBusy(false) }
  }
  return <section aria-label="Profile picture" className="max-w-xl space-y-5">
    <p className="text-sm text-muted-foreground">Your picture appears beside your name in Team, people lists, invites and agent messages. It changes only your account on this network.</p>
    <div className="flex items-center gap-3"><Avatar name={user?.username || 'You'} {...saved} className="h-14 w-14 text-xl" /><span className="font-medium">{user?.username || 'Your account'}</span></div>
    <fieldset disabled={busy || !loaded} className="space-y-4">
      <legend className="sr-only">Choose your picture</legend>
      <label className="block space-y-2 text-sm"><span>Upload a photo</span><input type="file" accept="image/jpeg,image/png,image/webp" className="block w-full text-sm" onChange={event => { void choosePhoto(event.target.files?.[0]); event.target.value = '' }} /></label>
      {photo && <div className="space-y-2"><canvas ref={canvas} width={256} height={256} aria-label="Square photo crop preview" className="h-40 w-40 rounded-lg" />{([['Zoom', zoom, setZoom, 1, 8], ['Horizontal position', x, setX, 0, 1], ['Vertical position', y, setY, 0, 1]] as const).map(([label, value, change, min, max]) => <label key={label} className="flex items-center gap-3 text-sm">{label}<input type="range" min={min} max={max} step="0.01" value={value} onChange={event => change(Number(event.target.value))} /></label>)}</div>}
      <div><p className="mb-2 text-sm">Or choose an icon</p><div className="flex gap-2">{['person', 'rocket', 'star', 'leaf'].map(value => <button key={value} type="button" aria-label={`Use ${value} icon`} aria-pressed={!photo && icon === value} onClick={() => { read.current++; setPhoto(undefined); setIcon(value) }} className={`rounded-full p-1 focus-visible:ring-2 ${!photo && icon === value ? 'ring-2 ring-primary' : ''}`}><Avatar name="You" avatar_icon={value} avatar_color={colour} className="h-10 w-10" /></button>)}</div></div>
      <div className="flex flex-wrap gap-2">{['blue', 'purple', 'green', 'orange'].map(value => <button key={value} type="button" aria-pressed={colour === value} onClick={() => setColour(value)} className={`rounded-md border px-3 py-1 text-sm ${colour === value ? 'border-primary' : 'border-border'}`}>{value}</button>)}</div>
      <div className="flex gap-3"><button type="button" onClick={() => void save()} className="rounded-md bg-primary px-3 py-2 text-sm text-primary-foreground">{busy ? 'Saving…' : 'Save picture'}</button><button type="button" onClick={() => void save(true)} className="rounded-md border border-border px-3 py-2 text-sm">Remove picture</button></div>
    </fieldset>
    {notice && <p role="status" className="text-sm">{notice}</p>}{error && <p role="alert" className="text-sm text-destructive">{error}</p>}
    <p className="text-xs text-muted-foreground">You can also ask your own agent: “Use the rocket icon for my profile picture”, or “Set my profile picture to this attached image”.</p>
  </section>
}
