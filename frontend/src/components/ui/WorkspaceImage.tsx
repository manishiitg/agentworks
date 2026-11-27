import { LoadingIndicator } from './LoadingIndicator'
import { useEffect, useState, type ReactNode } from 'react'
import { apiUrlForPath, getAuthToken } from '../../services/api'

// <img> cannot attach the app's Bearer header. Fetch privately, then display a
// revocable Blob URL instead of putting an account token into the image URL.
export function WorkspaceImage({ path, alt, className, renderImage, noun = 'image' }: { path: string; alt?: string; className?: string; renderImage?: (src: string) => ReactNode; noun?: 'image' | 'video' }) {
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
    void fetch(apiUrlForPath(`/api/public/file?${query}`, path), { headers: token ? { Authorization: `Bearer ${token}` } : {}, signal: controller.signal })
      .then(async response => {
        if (response.ok) return response.blob()
        // The server says why (for example: outside the folders attached to your chats).
        const message = await response.json().then((body: { error?: { message?: string } }) => body?.error?.message, () => undefined)
        throw new Error(message || (response.status === 404 ? `${noun === 'video' ? 'Video' : 'Image'} not found` : response.status === 401 || response.status === 403 ? `You don’t have access to this ${noun}` : `${noun === 'video' ? 'Video' : 'Image'} could not be loaded`))
      })
      .then(blob => { if (controller.signal.aborted) return; objectURL = URL.createObjectURL(blob); setSrc(objectURL) })
      .catch(error => { if (!controller.signal.aborted) setError(error instanceof Error ? error.message : `${noun === 'video' ? 'Video' : 'Image'} could not be loaded`) })
    return () => { controller.abort(); if (objectURL) URL.revokeObjectURL(objectURL) }
  }, [path])
  return src ? (renderImage ? renderImage(src) : <img src={src} alt={alt} className={className} />) : <span role="status" aria-label={error || `Loading ${noun}`} className={`inline-flex min-h-16 min-w-20 items-center justify-center gap-1.5 rounded bg-muted px-2 text-xs text-muted-foreground ${className ?? ''}`}>
    {!error && <LoadingIndicator aria-hidden="true" className="h-3.5 w-3.5 shrink-0" />}
    {error || `Loading ${noun}…`}
  </span>
}
