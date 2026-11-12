import { useEffect, useRef, useState } from 'react'
import { Download, Loader2, X } from 'lucide-react'
import { WorkspaceImage } from './WorkspaceImage'
import ModalPortal from './ModalPortal'

function ImagePreview({ src, alt, className }: { src: string; alt: string; className?: string }) {
  const [loaded, setLoaded] = useState(false)
  const [failed, setFailed] = useState(false)
  const [open, setOpen] = useState(false)
  const dialog = useRef<HTMLDialogElement>(null)
  useEffect(() => { if (open) dialog.current?.showModal() }, [open])
  return <span className="inline-flex min-w-0 max-w-full flex-col align-middle">
    {!loaded && !failed && <span role="status" className="inline-flex items-center gap-2 p-3 text-xs text-muted-foreground"><Loader2 aria-hidden className="h-4 w-4 animate-spin" />Loading image…</span>}
    {failed ? <span role="status" className="inline-block p-3 text-xs text-muted-foreground">Image could not be loaded</span> :
      <button type="button" disabled={!loaded} className="inline-block max-w-full cursor-zoom-in align-middle" aria-label={`Enlarge image${alt ? `: ${alt}` : ''}`} onClick={event => { event.preventDefault(); event.stopPropagation(); setOpen(true) }}>
        <img src={src} alt={alt} loading="lazy" referrerPolicy="no-referrer" onLoad={() => setLoaded(true)} onError={() => setFailed(true)} className={`${className ?? 'max-h-80 max-w-full rounded-lg object-contain'} ${!loaded ? 'opacity-0' : ''}`} />
      </button>}
    {open && <ModalPortal><dialog ref={dialog} onClose={() => setOpen(false)} onClick={event => { event.stopPropagation(); if (event.target === event.currentTarget) dialog.current?.close() }} className="fixed inset-0 m-auto max-h-[95dvh] w-fit max-w-[95vw] overflow-auto rounded-xl border border-border bg-background p-4 text-foreground shadow-xl backdrop:bg-black/80">
      <div className="mb-3 flex items-center justify-between gap-4">
        <span className="truncate text-sm">{alt || 'Image'}</span>
        <div className="flex shrink-0 items-center gap-3">
          <a href={src} download referrerPolicy="no-referrer" target="_blank" rel="noopener noreferrer" className="inline-flex items-center gap-1 text-xs" title="Download image (remote images may open in a new tab)"><Download aria-hidden className="h-4 w-4" />Download</a>
          <button type="button" onClick={() => dialog.current?.close()} aria-label="Close image" className="rounded p-1 hover:bg-muted"><X aria-hidden className="h-5 w-5" /></button>
        </div>
      </div>
      <img src={src} alt={alt} referrerPolicy="no-referrer" className="max-h-[80dvh] max-w-full object-contain" />
    </dialog></ModalPortal>}
  </span>
}

// The caller resolves workspace paths; the existing file endpoint still decides access.
export function ChatImage({ src, alt = '', workspace = false, className }: { src: string; alt?: string; workspace?: boolean; className?: string }) {
  return workspace
    ? <WorkspaceImage key={src} path={src} alt={alt} renderImage={resolved => <ImagePreview key={resolved} src={resolved} alt={alt} className={className} />} />
    : <ImagePreview key={src} src={src} alt={alt} className={className} />
}
