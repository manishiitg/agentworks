import { useEffect, useRef, useState } from 'react'
import { X } from 'lucide-react'
import { agentApi } from '../../services/api'
import type { FileContextItem } from '../../stores/types'
import { isChatIdentityCurrent } from '../../utils/chatIdentity'
import { WorkspaceImage } from '../ui/WorkspaceImage'

export function ContextAttachmentPreview({ file, identity, onClose }: { file: FileContextItem; identity: number; onClose: () => void }) {
  const dialog = useRef<HTMLDialogElement>(null)
  const [content, setContent] = useState<Record<string, unknown>>()
  const [failed, setFailed] = useState(false)
  useEffect(() => {
    let current = true
    dialog.current?.showModal()
    setContent(undefined); setFailed(false)
    void agentApi.getPlannerFileContent(file.path).then(result => {
      if (!current || !isChatIdentityCurrent(identity)) return
      if (!result.success || typeof result.data?.content !== 'string') throw new Error('unavailable')
      const parsed: unknown = JSON.parse(result.data.content.slice(0, 65536))
      if (!parsed || typeof parsed !== 'object' || Array.isArray(parsed)) throw new Error('invalid context')
      setContent(parsed as Record<string, unknown>)
    }).catch(() => { if (current && isChatIdentityCurrent(identity)) setFailed(true) })
    return () => { current = false }
  }, [file.path, identity])
  const text = (key: string) => typeof content?.[key] === 'string' ? (content[key] as string).slice(0, 16384) : ''
  if (!isChatIdentityCurrent(identity)) return null
  return <dialog ref={dialog} onClose={onClose} onKeyDown={event => event.stopPropagation()} aria-label="Attached context" className="fixed inset-0 m-auto max-h-[80dvh] w-[min(36rem,calc(100vw-2rem))] overflow-auto rounded-xl border border-border bg-background p-4 text-foreground shadow-xl backdrop:bg-black/50">
    <div className="mb-3 flex items-center justify-between gap-3"><h2 className="text-sm font-medium">{file.context?.label || 'Attached context'}</h2><button type="button" onClick={() => dialog.current?.close()} aria-label="Close attached context" className="rounded p-1 text-muted-foreground hover:bg-muted"><X className="h-4 w-4" /></button></div>
    {file.context?.previewPath && <WorkspaceImage path={file.context.previewPath} alt="Selected page element" className="mb-3 max-h-48 max-w-full rounded border border-border object-contain" />}
    {failed ? <p role="alert" className="text-xs text-destructive">This context could not load. Remove it and add the selection again.</p> : !content ? <p role="status" className="text-xs text-muted-foreground">Loading context…</p> : <div className="space-y-3 text-xs">
      {['url', 'selector', 'command'].map(key => text(key) && <div key={key}><div className="mb-1 font-medium capitalize text-muted-foreground">{key}</div><p className="break-all">{text(key)}</p></div>)}
      <pre className="whitespace-pre-wrap break-words rounded bg-muted/50 p-3 font-mono text-[11px]">{text('text')}</pre>
    </div>}
  </dialog>
}
