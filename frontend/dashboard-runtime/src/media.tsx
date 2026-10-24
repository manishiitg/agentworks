import React, { useEffect, useRef, useState, type ReactNode } from 'react'

export type DashboardAsset = { path: string; name: string; size: number; content_type?: string }
export type DashboardPresentation = {
  id: string; kind: string; title: string; revision?: number; updated_at?: string
  payload: Record<string, any>
}

type AssetAPI = {
  fileUrl: (path: string) => Promise<string | null>
  mediaUrl: (path: string) => Promise<string | null>
  getText: (path: string) => Promise<string | null>
  renderMarkdown: (text: string) => string
  uploadFile: (file: File) => Promise<DashboardAsset>
}

/** Read from the host on every operation: a live refresh can replace its adapter. */
const assetAPI = (): AssetAPI => (window as any).report

export function UploadZone({ onUploaded, label = 'Add references', className = '' }: {
  onUploaded?: (assets: DashboardAsset[]) => void | Promise<void>; label?: string; className?: string
}) {
  const input = useRef<HTMLInputElement>(null)
  const [busy, setBusy] = useState(false)
  const [status, setStatus] = useState('')
  const [error, setError] = useState('')
  const uploading = useRef(false)
  const upload = async (files: File[]) => {
    if (!files.length || uploading.current) return
    uploading.current = true; setBusy(true); setError(''); setStatus('')
    const saved: DashboardAsset[] = []
    const failures: string[] = []
    try {
      for (const file of files) {
        setStatus(`Adding ${file.name}…`)
        try {
          const api = assetAPI()
          if (typeof api.uploadFile !== 'function') throw new Error('Open the live dashboard to upload files.')
          saved.push(await api.uploadFile(file))
        }
        catch (cause) { failures.push(`${file.name}: ${cause instanceof Error ? cause.message : 'Upload failed'}`) }
      }
      if (saved.length) await onUploaded?.(saved)
      setStatus(saved.length ? `${saved.length} file${saved.length === 1 ? '' : 's'} uploaded.` : '')
      setError(failures.join('\n'))
    } catch (cause) {
      setError(`Files were uploaded, but the next action failed: ${cause instanceof Error ? cause.message : String(cause)}`)
    } finally { uploading.current = false; setBusy(false) }
  }
  return <section className={`rounded-xl border border-dashed border-border p-4 ${className}`}
    onDragOver={event => event.preventDefault()}
    onDrop={event => { event.preventDefault(); void upload(Array.from(event.dataTransfer.files)) }}>
    <input ref={input} type="file" multiple hidden accept="image/png,image/jpeg,image/webp,image/gif,image/avif,video/*,audio/*,.pdf,.txt,.md,.csv,.json,.docx,.xlsx,.pptx"
      onChange={event => { const files = Array.from(event.target.files ?? []); event.target.value = ''; void upload(files) }} />
    <div className="flex flex-wrap items-center justify-between gap-3">
      <p className="text-sm text-muted-foreground">Drop images, clips, audio or documents here.</p>
      <button type="button" disabled={busy} onClick={() => input.current?.click()} className="dashboard-button">{busy ? 'Adding…' : label}</button>
    </div>
    {status && <p role="status" className="mt-2 text-sm text-muted-foreground">{status}</p>}
    {error && <p role="alert" className="mt-2 whitespace-pre-wrap text-sm text-destructive">{error}</p>}
  </section>
}

function presentationPath(item: DashboardPresentation): string {
  return item.payload.path || item.payload.image_path || ''
}

/** A stable player only reloads when this result changes, not on unrelated DB notices. */
export function MediaPreview({ item }: { item: DashboardPresentation }) {
  const source = presentationPath(item)
  const media = item.kind === 'media.video' || item.kind === 'media.audio'
  const document = item.kind === 'document.markdown'
  const [url, setURL] = useState<string | null>(null)
  const [text, setText] = useState('')
  const [error, setError] = useState('')
  const [retry, setRetry] = useState(0)
  const [remoteFailed, setRemoteFailed] = useState(false)
  const remote = media && typeof item.payload.source_url === 'string' && item.payload.source_url.startsWith('https://') ? item.payload.source_url : ''
  const [loading, setLoading] = useState(true)
  useEffect(() => {
    let active = true
    let blob: string | null = null
    setError(''); setLoading(true); setRemoteFailed(false)
    const load = async () => {
      if (document) {
        const content = item.payload.markdown ?? await assetAPI().getText(source)
        if (content == null) throw new Error('This saved document could not be opened.')
        const next = URL.createObjectURL(new Blob([content], { type: 'text/markdown;charset=utf-8' }))
        if (!active) URL.revokeObjectURL(next)
        else { blob = next; setText(content); setURL(next) }
      } else {
        const next = media ? await assetAPI().mediaUrl(source) : await assetAPI().fileUrl(source)
        if (!next) throw new Error('This saved result could not be opened.')
        if (next.startsWith('blob:')) { if (!active) URL.revokeObjectURL(next); else blob = next }
        if (active) setURL(next)
      }
    }
    void load().catch(cause => { if (active) setError(cause instanceof Error ? cause.message : String(cause)) })
      .finally(() => { if (active) setLoading(false) })
    return () => { active = false; if (blob) URL.revokeObjectURL(blob) }
  }, [source, item.id, item.revision, media, document, retry])
  return <article className="overflow-hidden rounded-xl border border-border bg-card">
    <header className="flex flex-wrap items-center justify-between gap-2 border-b border-border p-3">
      <h3 className="font-semibold">{item.title}</h3>
      {url && <a href={url} download={source.split('/').pop() || 'document.md'} className="text-sm text-primary underline">Download</a>}
    </header>
    {loading && !url && <p role="status" className="p-4 text-sm text-muted-foreground">Opening result…</p>}
    {error ? <div role="alert" className="p-4 text-sm text-destructive">{error} <button type="button" className="underline" onClick={() => setRetry(value => value + 1)}>Retry</button></div>
      : document ? <div className="prose max-w-none p-4 dark:prose-invert" dangerouslySetInnerHTML={{ __html: assetAPI().renderMarkdown(text) }} />
      : url && media ? item.kind === 'media.audio'
        ? <audio controls preload="metadata" src={url} className="w-full p-3" onError={() => setError('This audio could not be played.')} />
        : <video controls playsInline preload="metadata" src={remote && !remoteFailed ? remote : url} className="aspect-video w-full bg-black object-contain" onError={() => { if (remote && !remoteFailed) setRemoteFailed(true); else setError('This video could not be played.') }} />
      : url && <img src={url} alt={item.title} style={{ maxHeight: 'min(70vh, 36rem)' }} className="w-full object-contain" onError={() => setError('This reference could not be opened.')} />}
    {item.payload.spec && <div style={{ maxHeight: '18rem', overflow: 'auto' }} className="prose max-w-none border-t border-border p-4 text-sm dark:prose-invert" dangerouslySetInnerHTML={{ __html: assetAPI().renderMarkdown(String(item.payload.spec)) }} />}
    {item.payload.note && <p className="border-t border-border p-3 text-sm text-muted-foreground">{item.payload.note}</p>}
    {item.kind === 'media.video' && <p className="border-t border-border px-3 py-2 text-xs text-muted-foreground">{item.payload.verdict === 'placeholder-pass' ? 'Placeholder · Quality checked' : item.payload.qa_report_path && item.payload.verdict === 'pass' ? 'Quality checked · Final' : 'Preview · Awaiting final quality check'}</p>}
  </article>
}

/** Data comes from loadData; live refreshes preserve selection and the mounted player. */
export function MediaGallery({ items, emptyText = 'Results will appear here as they are created.', renderActions }: {
  items: DashboardPresentation[]; emptyText?: string; renderActions?: (item: DashboardPresentation) => ReactNode
}) {
  const [selected, setSelected] = useState<string | null>(null)
  const newest = items[0]?.id ?? null
  const newestRevision = newest ? newest + ':' + (items[0]?.revision ?? 0) : ''
  const previousNewest = useRef(newestRevision)
  useEffect(() => {
    if (newestRevision !== previousNewest.current) { previousNewest.current = newestRevision; if (newest) setSelected(newest) }
  }, [newest, newestRevision])
  const current = items.find(item => item.id === selected) ?? items[0]
  if (!current) return <p className="rounded-xl border border-dashed border-border p-8 text-center text-sm text-muted-foreground">{emptyText}</p>
  return <section className="space-y-3">
    <nav aria-label="Choose a result" className="flex gap-2 overflow-x-auto pb-1">
      {items.map(item => <button key={item.id} type="button" aria-pressed={item.id === current.id} onClick={() => setSelected(item.id)}
        className={`shrink-0 rounded-lg border px-3 py-2 text-sm ${item.id === current.id ? 'border-primary bg-primary/10 text-primary' : 'border-border text-muted-foreground'}`}>{item.title}</button>)}
    </nav>
    <MediaPreview key={current.id} item={current} />
    {renderActions?.(current)}
  </section>
}
