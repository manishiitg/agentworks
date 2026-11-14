import { useState } from 'react'
import type { FileContextItem } from '../stores/types'
import { captureChatIdentity, isChatIdentityCurrent } from '../utils/chatIdentity'
import { ContextAttachmentPreview } from './chat/ContextAttachmentPreview'
import { FileText, Folder, X } from 'lucide-react'
import { WorkspaceImage } from './ui/WorkspaceImage'


interface FileContextDisplayProps {
  attachmentMode?: boolean
  files: FileContextItem[]
  onRemoveFile: (path: string) => void
  onClearAll: () => void
  agentMode: 'multi-agent' | 'workflow'
  isRequiredFolderSelected: boolean
}

export default function FileContextDisplay({ attachmentMode = false, files, onRemoveFile, onClearAll, agentMode, isRequiredFolderSelected }: FileContextDisplayProps) {
  const [preview, setPreview] = useState<{ path: string; identity: number }>()
  const previewFile = preview && isChatIdentityCurrent(preview.identity) ? files.find(file => file.path === preview.path) : undefined
  if (files.length === 0) {
    return null
  }

  if (attachmentMode) {
    const groupedImages = new Set(files.flatMap(file => file.context?.previewPath ? [file.context.previewPath] : []))
    return <div className="px-2 pt-2 pb-1">
      <div aria-label="Attachments" className="flex gap-2 overflow-x-auto">
      {files.filter(file => !groupedImages.has(file.path)).map(file => {
        const name = file.context?.label || file.name || file.path.split('/').filter(Boolean).pop() || 'File'
        const image = file.type === 'file' && /\.(png|jpe?g|gif|webp|avif|bmp)$/i.test(file.path)
        return <div key={file.path} title={name} className={`relative shrink-0 overflow-hidden rounded-xl border border-border bg-muted ${image ? 'h-24 w-24' : 'flex h-14 max-w-56 items-center gap-2 pl-3 pr-9'}`}>
          {file.context ? <button type="button" onClick={() => setPreview({ path: file.path, identity: captureChatIdentity() })} className="flex min-w-0 items-center gap-2 text-left" aria-label={`View ${name}`}>
            {file.context.previewPath && files.some(child => child.path === file.context?.previewPath) ? <WorkspaceImage path={file.context.previewPath} alt="Selected page element" className="h-11 w-14 rounded object-cover" /> : <FileText className="h-5 w-5 shrink-0 text-muted-foreground" />}
            <span className="truncate text-sm">{name}</span>
          </button> : image ? <WorkspaceImage path={file.path} alt={name} className="h-full w-full object-cover" /> : <>
            {file.type === 'folder' ? <Folder className="h-5 w-5 shrink-0 text-muted-foreground" /> : <FileText className="h-5 w-5 shrink-0 text-muted-foreground" />}
            <span className="truncate text-sm">{name}</span>
          </>}
          <button type="button" onClick={() => onRemoveFile(file.path)} aria-label={`Remove ${name}`} title={`Remove ${name}`} className="absolute right-1 top-1 flex h-6 w-6 items-center justify-center rounded-full border border-border bg-background text-foreground shadow-sm hover:bg-muted focus-visible:outline focus-visible:outline-2 focus-visible:outline-ring">
            <X className="h-3.5 w-3.5" />
          </button>
        </div>
      })}
      </div>
      {previewFile && preview && <ContextAttachmentPreview key={preview.path} file={previewFile} identity={preview.identity} onClose={() => setPreview(undefined)} />}
    </div>
  }

  return (
    <div className={`border rounded px-1.5 py-0.5 mb-1 ${
      agentMode === 'workflow' && !isRequiredFolderSelected
        ? 'bg-amber-500/10 border-amber-500/20'
        : 'bg-muted border-border'
    }`}>
      <div className="flex items-center gap-1.5 flex-wrap">
        <span className={`text-xs font-medium ${
          agentMode === 'workflow' && !isRequiredFolderSelected
            ? 'text-amber-700 dark:text-amber-400'
            : 'text-muted-foreground'
        }`}>
          {agentMode === 'workflow' && !isRequiredFolderSelected ? 'Select a project folder:' : 'For next message:'}
        </span>
        {files.map((file, index) => (
          <div key={file.path} className="flex items-center gap-0.5">
            <span className="inline-flex min-w-0 items-center gap-1 text-xs text-foreground">
              {file.type === 'folder' ? <Folder className="h-3 w-3 shrink-0" aria-hidden="true" /> : <FileText className="h-3 w-3 shrink-0" aria-hidden="true" />}
              <span className="max-w-[14rem] truncate">{file.name || file.path.split('/').filter(Boolean).pop() || (file.type === 'folder' ? 'Folder' : 'File')}</span>
            </span>
            <button
              onClick={() => onRemoveFile(file.path)}
              className="p-0.5 hover:bg-destructive/10 rounded text-destructive"
              title={`Remove ${file.name || file.type}`}
              aria-label={`Remove ${file.name || file.type}`}
            >
              <X className="w-2 h-2" />
            </button>
            {index < files.length - 1 && (
              <span className="text-xs text-muted-foreground">•</span>
            )}
          </div>
        ))}
        <button
          onClick={onClearAll}
          className="text-xs text-muted-foreground hover:text-foreground hover:underline ml-0.5"
        >
          Clear
        </button>
      </div>
    </div>
  )
}
