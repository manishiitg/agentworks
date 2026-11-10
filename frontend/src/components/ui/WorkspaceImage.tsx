import { Loader2 } from 'lucide-react'
import { useEffect, useState } from 'react'
import { getApiBaseUrl, getAuthToken } from '../../services/api'

// <img> cannot attach the app's Bearer header. Fetch privately, then display a
// revocable Blob URL instead of putting an account token into the image URL.
export function WorkspaceImage({ path, alt, className }: { path: string; alt?: string; className?: string }) {
  const [src, setSrc] = useState<string>()
  const [failed, setFailed] = useState(false)
  useEffect(() => {
    const controller = new AbortController()
    let objectURL: string | undefined
    setSrc(undefined)
    setFailed(false)
    const bytes = new TextEncoder().encode(path)
    let raw = ''; for (const byte of bytes) raw += String.fromCharCode(byte)
    const query = new URLSearchParams({ path: btoa(raw) })
    const token = getAuthToken()
    void fetch(`${getApiBaseUrl()}/api/public/file?${query}`, { headers: token ? { Authorization: `Bearer ${token}` } : {}, signal: controller.signal })
      .then(async response => { if (!response.ok) throw new Error('Image unavailable'); return response.blob() })
      .then(blob => { if (controller.signal.aborted) return; objectURL = URL.createObjectURL(blob); setSrc(objectURL) })
      .catch(() => { if (!controller.signal.aborted) setFailed(true) })
    return () => { controller.abort(); if (objectURL) URL.revokeObjectURL(objectURL) }
  }, [path])
  return src ? <img src={src} alt={alt} className={className} /> : <span role="status" aria-label={failed ? 'Image unavailable' : 'Loading image'} className={`inline-flex min-h-16 min-w-20 items-center justify-center gap-1.5 rounded bg-muted px-2 text-xs text-muted-foreground ${className ?? ''}`}>
    {!failed && <Loader2 aria-hidden="true" className="h-3.5 w-3.5 shrink-0 animate-spin" />}
    {failed ? 'Image unavailable' : 'Loading image…'}
  </span>
}
