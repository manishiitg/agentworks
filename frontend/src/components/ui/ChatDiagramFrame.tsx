import { useRef, useState, type ReactNode, type RefObject } from 'react'
import { Check, Copy, Download, Loader2 } from 'lucide-react'
import { diagramPng, downloadDiagram } from './exportChatDiagram'

export function ChatDiagramFrame({ title, width, height, kind = 'diagram', viewportRef, description, children }: {
  title: string; width: number; height: number; kind?: 'diagram' | 'swimlane' | 'sequence'
  viewportRef?: RefObject<HTMLDivElement | null>; description: ReactNode; children: ReactNode
}) {
  const figure = useRef<HTMLElement>(null)
  const [exporting, setExporting] = useState(false)
  const [exportStatus, setExportStatus] = useState('')
  const [downloadOnly, setDownloadOnly] = useState(false)
  const canCopy = typeof ClipboardItem !== 'undefined' && typeof navigator !== 'undefined' && !!navigator.clipboard?.write && !downloadOnly
  const exportImage = async () => {
    if (!figure.current || exporting) return
    setExporting(true)
    setExportStatus('')
    const png = diagramPng(figure.current, width, height)
    void png.catch(() => {})
    try {
      if (canCopy) {
        // Start in the click gesture; rendering supplies the PNG later.
        await navigator.clipboard.write([new ClipboardItem({ 'image/png': png })])
        setExportStatus('Copied image. Paste it into Slack.')
      } else {
        downloadDiagram(await png, title)
        setExportStatus('Image downloaded.')
      }
    } catch {
      if (canCopy && await png.then(() => true, () => false)) {
        setDownloadOnly(true)
        setExportStatus('Copy unavailable. Download the image instead.')
      } else setExportStatus('Could not create the image. Try again.')
    } finally { setExporting(false) }
  }
  return <figure ref={figure} className="not-prose my-4 w-full min-w-0 max-w-full overflow-hidden rounded-2xl border border-border bg-muted/20 text-foreground" data-chat-element={kind}>
    <figcaption className="flex flex-wrap items-center gap-2 border-b border-border px-4 py-3 text-sm font-semibold">
      <span className="min-w-0 flex-1">{title}</span>
      <span data-diagram-export-controls className="flex flex-wrap items-center gap-2">
        <span role="status" className="text-xs font-normal text-muted-foreground">{exportStatus}</span>
        <button type="button" disabled={exporting} onClick={() => { void exportImage() }} aria-label={canCopy ? 'Copy diagram as image' : 'Download diagram image'} title={canCopy ? 'Copy image to paste into Slack' : 'Download PNG image'} className="inline-flex shrink-0 items-center gap-1.5 rounded-md px-2 py-1 text-xs font-medium text-muted-foreground hover:bg-muted hover:text-foreground disabled:opacity-50">
          {exporting ? <Loader2 className="h-3.5 w-3.5 animate-spin" /> : !canCopy ? <Download className="h-3.5 w-3.5" /> : exportStatus.startsWith('Copied') ? <Check className="h-3.5 w-3.5" /> : <Copy className="h-3.5 w-3.5" />}
          {canCopy ? 'Copy image' : 'Download image'}
        </button>
      </span>
    </figcaption>
    <div ref={viewportRef} data-diagram-viewport className="max-h-[48rem] overflow-auto" role="region" aria-label={title} tabIndex={0}>
      <div className="relative mx-auto" style={{ width, height }}>{children}</div>
    </div>
    <div className="sr-only">{description}</div>
  </figure>
}
