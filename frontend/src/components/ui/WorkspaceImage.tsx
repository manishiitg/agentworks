import { Loader2 } from 'lucide-react'
import { useEffect, useState, type ReactNode } from 'react'
import { getApiBaseUrl, getAuthToken } from '../../services/api'

// <img> cannot attach the app's Bearer header. Fetch privately, then display a
// revocable Blob URL instead of putting an account token into the image URL.
export function WorkspaceImage({ path, alt, className, renderImage }: { path: string; alt?: string; className?: string; renderImage?: (src: string) => ReactNode }) {
  const [src, setSrc] = useState<string>()
  const [error, setError] = useState('')
  useEffect(() => {
    const controller = new AbortController()
    let objectURL: string | undefined
    setSrc(undefined)
    setError('')
    const bytes = new TextEncoder().encode(path)
    let raw = ''; for (const byte of bytes) raw += String.fromCharCode(byte)
    const query = new URLSearchParams({ path: btoa(raw) })
    const token = getAuthToken()
    void fetch(`${getApiBaseUrl()}/api/public/file?${query}`, { headers: token ? { Authorization: `Bearer ${token}` } : {}, signal: controller.signal })
      .then(async response => { if (!response.ok) throw new Error(response.status === 404 ? 'Image not found' : response.status === 401 || response.status === 403 ? 'You don’t have access to this image' : 'Image could not be loaded'); return response.blob() })
      .then(blob => { if (controller.signal.aborted) return; objectURL = URL.createObjectURL(blob); setSrc(objectURL) })
      .catch(error => { if (!controller.signal.aborted) setError(error instanceof Error ? error.message : 'Image could not be loaded') })
    return () => { controller.abort(); if (objectURL) URL.revokeObjectURL(objectURL) }
  }, [path])
  return src ? (renderImage ? renderImage(src) : <img src={src} alt={alt} className={className} />) : <span role="status" aria-label={error || 'Loading image'} className={`inline-flex min-h-16 min-w-20 items-center justify-center gap-1.5 rounded bg-muted px-2 text-xs text-muted-foreground ${className ?? ''}`}>
    {!error && <Loader2 aria-hidden="true" className="h-3.5 w-3.5 shrink-0 animate-spin" />}
    {error || 'Loading image…'}
  </span>
}
