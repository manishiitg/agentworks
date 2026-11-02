import { useState } from 'react'
import api from '../../services/api'

// A Mac Crew's chat runs on the owner's Mac; its files are not on the server.
// Tapping a changed file asks the Mac for it through the chat's file route
// (PLAT-856-509 contract op=file) and shows it inline, so a phone needs no
// popup or download. The server and Mac decide access, size and errors.
export const isMacChatSession = (sessionId: string) => sessionId.startsWith('paired-mac-')

type Loaded = { text?: string; image?: string; note?: string; error?: string }

const IMAGE = /\.(png|jpe?g|gif|webp|svg)$/i

function decode(base64: string, path: string): Loaded {
  const bytes = Uint8Array.from(atob(base64), c => c.charCodeAt(0))
  if (IMAGE.test(path)) {
    const type = path.toLowerCase().endsWith('.svg') ? 'image/svg+xml' : `image/${path.split('.').pop()!.toLowerCase().replace('jpg', 'jpeg')}`
    return { image: URL.createObjectURL(new Blob([bytes], { type })) }
  }
  try {
    return { text: new TextDecoder('utf-8', { fatal: true }).decode(bytes) }
  } catch {
    return { note: 'This file cannot be shown here. Open it on your Mac.' }
  }
}

export function MacFileRow({ sessionId, path, label }: { sessionId: string; path: string; label: string }) {
  const [open, setOpen] = useState(false)
  const [loaded, setLoaded] = useState<Loaded | null>(null)
  const toggle = async () => {
    if (open) { setOpen(false); return }
    setOpen(true)
    if (loaded && !loaded.error) return
    setLoaded(null)
    try {
      const { data } = await api.get<{ content_base64: string }>(`/api/sessions/${encodeURIComponent(sessionId)}/file`, { params: { path } })
      setLoaded(decode(data.content_base64 || '', path))
    } catch (cause) {
      const body = (cause as { response?: { data?: { error?: string | { message?: string } } } })?.response?.data?.error
      setLoaded({ error: (typeof body === 'string' ? body : body?.message) || 'Could not open this file from your Mac. Try again.' })
    }
  }
  return <li>
    <button type="button" aria-expanded={open} onClick={() => void toggle()} className="min-h-11 w-full break-all py-2 text-left font-mono underline-offset-2 hover:underline sm:min-h-0 sm:py-0.5">{label}</button>
    {open && <div className="mb-2 mt-1 max-h-[60dvh] overflow-auto rounded border border-border bg-background p-2">
      {!loaded && <p role="status" className="text-muted-foreground">Opening from your Mac…</p>}
      {loaded?.error && <p role="alert">{loaded.error}</p>}
      {loaded?.note && <p className="text-muted-foreground">{loaded.note}</p>}
      {loaded?.image && <img src={loaded.image} alt={label} className="max-w-full" />}
      {loaded?.text !== undefined && <pre className="whitespace-pre-wrap break-words font-mono text-[11px] leading-snug">{loaded.text}</pre>}
    </div>}
  </li>
}
