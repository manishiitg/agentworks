import { useEffect, useState } from 'react'
import { FileText, Loader2 } from 'lucide-react'

/** Immediate local preview only; a file becomes an attachment after the server accepts it. */
export function UploadingAttachments({ files }: { files: File[] }) {
  const [previews, setPreviews] = useState<(string | undefined)[]>([])
  useEffect(() => {
    const urls = files.map(file => file.type.startsWith('image/') ? URL.createObjectURL(file) : undefined)
    setPreviews(urls)
    return () => { urls.forEach(url => { if (url) URL.revokeObjectURL(url) }) }
  }, [files])
  return <div className="px-2 pt-2 pb-1" aria-busy="true">
    <div role="status" className="mb-2 flex items-center gap-1.5 text-xs text-muted-foreground">
      <Loader2 aria-hidden="true" className="h-3.5 w-3.5 shrink-0 animate-spin" />
      <span className="min-w-0 truncate">{files.length === 1 ? `Uploading ${files[0].name}…` : `Uploading ${files.length} files…`}</span>
    </div>
    <div className="flex gap-2 overflow-x-auto">
      {files.map((file, index) => <div key={index} title={file.name}
        className="relative flex h-24 w-24 shrink-0 items-center justify-center overflow-hidden rounded-xl border border-border bg-muted">
        {previews[index] ? <img src={previews[index]} alt={file.name} className="h-full w-full object-cover opacity-60" /> : <FileText aria-hidden="true" className="h-6 w-6 text-muted-foreground" />}
        <span className="absolute inset-x-0 bottom-0 truncate bg-background/95 px-1.5 py-1 text-[10px] text-foreground">{file.name}</span>
      </div>)}
    </div>
  </div>
}
