const imageTypes: Record<string, string> = { png: 'image/png', jpg: 'image/jpeg', jpeg: 'image/jpeg', gif: 'image/gif', webp: 'image/webp', svg: 'image/svg+xml' }

// Brain stores any file (images, PDFs, decks, spreadsheets): show images inline and offer every file as a download.
export function BinaryFilePreview({ name, base64, size }: { name: string; base64: string; size?: number }) {
  const extension = name.split('.').pop()?.toLowerCase() || ''
  const image = imageTypes[extension]
  // SVG can carry script; it is only ever shown through <img>, which does not run it.
  const href = `data:${image || 'application/octet-stream'};base64,${base64}`
  return <div className="mt-2 space-y-2">
    {image && <img src={href} alt={name} className="max-h-96 max-w-full rounded border border-border" />}
    <a href={href} download={name} className="inline-block text-primary hover:underline">Download {name}{size ? ` (${Math.max(1, Math.round(size / 1024))} KB)` : ''}</a>
  </div>
}
