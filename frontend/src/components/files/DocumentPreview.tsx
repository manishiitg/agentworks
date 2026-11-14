import { lazy, Suspense, useEffect, useState } from 'react'
import { CsvRenderer } from '../ui/CsvRenderer'
import { NotebookPreview } from './NotebookPreview'
import { AUDIO_MIME_TYPES, VIDEO_MIME_TYPES, mimeForExtension } from '../../utils/fileTypes'

const Word = lazy(() => import('../ui/DocxRenderer').then(m => ({ default: m.DocxRenderer })))
const Excel = lazy(() => import('../ui/XlsxRenderer').then(m => ({ default: m.XlsxRenderer })))
const Pdf = lazy(() => import('../ui/PdfRenderer').then(m => ({ default: m.PdfRenderer })))

export function hasDocumentPreview(name: string) {
  return /\.(pdf|docx|xlsx?|csv|ipynb|mp3|wav|m4a|aac|ogg|oga|flac|opus|webm|mp4|mov)$/i.test(name)
}

/** Format dispatch shared by Files, chat file links, reports and shared libraries.
 * Bytes come from the caller's existing authorized loader, never a guessed URL. */
export function DocumentPreview({ name, data, text = '' }: { name: string; data?: ArrayBuffer | null; text?: string }) {
  const ext = name.split('.').pop()?.toLowerCase()
  const mime = mimeForExtension(name, AUDIO_MIME_TYPES) || mimeForExtension(name, VIDEO_MIME_TYPES)
  const [media, setMedia] = useState('')
  useEffect(() => {
    if (!mime || !data) { setMedia(''); return }
    const url = URL.createObjectURL(new Blob([data], { type: mime }))
    setMedia(url)
    return () => URL.revokeObjectURL(url)
  }, [mime, data])
  return <Suspense fallback={<p className="p-4 text-sm text-muted-foreground">Loading preview…</p>}>
    {ext === 'csv' ? <CsvRenderer content={text} />
      : ext === 'ipynb' ? <NotebookPreview content={text} />
      : ext === 'docx' && data ? <Word data={data} />
      : (ext === 'xls' || ext === 'xlsx') && data ? <Excel data={data} />
      : ext === 'pdf' && data ? <div className="h-full min-h-96"><Pdf data={data} /></div>
      : media ? mime?.startsWith('audio/') ? <audio controls preload="metadata" src={media} className="my-4 w-full" />
        : <video controls preload="metadata" src={media} className="max-h-full max-w-full" />
      : <p className="p-4 text-sm text-muted-foreground">Preview unavailable. Download the file to open it.</p>}
  </Suspense>
}
