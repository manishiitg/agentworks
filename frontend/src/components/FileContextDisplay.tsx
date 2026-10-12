import { File, Folder, Image, X } from 'lucide-react'

interface FileContextItem {
  name: string
  path: string
  type: 'file' | 'folder'
}

interface FileContextDisplayProps {
  files: FileContextItem[]
  onRemoveFile: (path: string) => void
  onClearAll: () => void
  agentMode: 'multi-agent' | 'workflow'
  isRequiredFolderSelected: boolean
}

export default function FileContextDisplay({ files, onRemoveFile, onClearAll, agentMode, isRequiredFolderSelected }: FileContextDisplayProps) {
  if (files.length === 0) {
    return null
  }

  return (
    <div className="mb-1 flex min-w-0 flex-wrap items-center gap-1.5" aria-label="Attached files">
      {agentMode === 'workflow' && !isRequiredFolderSelected && (
        <span className="text-xs text-amber-700 dark:text-amber-400">Select an automation folder</span>
      )}
      {files.map(file => {
        const filename = (file.name || file.path).replace(/\\/g, '/').split('/').filter(Boolean).pop() || file.path
        const isImage = /\.(png|jpe?g|gif|webp|avif|bmp|svg)$/i.test(filename)
        const label = isImage && /^pasted-image-\d/i.test(filename) ? 'Pasted image' : filename
        const Icon = file.type === 'folder' ? Folder : isImage ? Image : File
        return (
          <div key={file.path} title={file.path} className="inline-flex max-w-full min-w-0 items-center gap-1 rounded-full border border-border bg-muted px-2 py-0.5 text-xs text-foreground">
            <Icon className="h-3 w-3 shrink-0 text-muted-foreground" aria-hidden="true" />
            <span className="max-w-48 truncate">{label}</span>
            <button
              type="button"
              onClick={() => onRemoveFile(file.path)}
              className="shrink-0 rounded-full p-1 text-muted-foreground hover:bg-destructive/10 hover:text-destructive focus-visible:outline focus-visible:outline-2 focus-visible:outline-ring"
              aria-label={`Remove ${filename}`}
              title={`Remove ${filename}`}
            >
              <X className="h-3 w-3" aria-hidden="true" />
            </button>
          </div>
        )
      })}
      {files.length > 1 && (
        <button
          type="button"
          onClick={onClearAll}
          className="text-xs text-muted-foreground hover:text-foreground hover:underline"
        >
          Clear all
        </button>
      )}
    </div>
  )
}
