import { DOCUMENT_PREVIEW_MAX_BYTES } from '../../components/files/readBoundedDocument'
import { useMemo } from 'react'
import { DocumentPreview, hasDocumentPreview } from '../../components/files/DocumentPreview'
const imageTypes: Record<string, string> = { png: 'image/png', jpg: 'image/jpeg', jpeg: 'image/jpeg', gif: 'image/gif', webp: 'image/webp', svg: 'image/svg+xml' }

// Brain stores any file (images, PDFs, decks, spreadsheets): show images inline and offer every file as a download.
export function BinaryFilePreview({ name, base64, size }: { name: string; base64: string; size?: number }) {
  const data = useMemo(() => base64.length > Math.ceil(DOCUMENT_PREVIEW_MAX_BYTES / 3) * 4 ? null : Uint8Array.from(atob(base64), c => c.charCodeAt(0)).buffer, [base64])
  const extension = name.split('.').pop()?.toLowerCase() || ''
  const image = imageTypes[extension]
  // SVG can carry script; it is only ever shown through <img>, which does not run it.
  const href = `data:${image || 'application/octet-stream'};base64,${base64}`
  return <div className="mt-2 space-y-2">
    {!data && <p className="text-xs text-muted-foreground">This file is too large for an inline preview. Download it to open it.</p>}
    {data && hasDocumentPreview(name) && <div className="h-[60vh] overflow-auto"><DocumentPreview key={name} name={name} data={data} text={/\.(csv|ipynb)$/i.test(name) ? new TextDecoder().decode(data) : ''} /></div>}
    {image && <img src={href} alt={name} className="max-h-96 max-w-full rounded border border-border" />}
    <a href={href} download={name} className="inline-block text-primary hover:underline">Download {name}{size ? ` (${Math.max(1, Math.round(size / 1024))} KB)` : ''}</a>
  </div>
}
