import { FileText, Folder, X } from 'lucide-react'

interface FileContextItem {
  name: string
  path: string
  type: 'file' | 'folder'
}

interface FileContextDisplayProps {
  attachmentMode?: boolean
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
          {agentMode === 'workflow' && !isRequiredFolderSelected ? 'Select an Automation folder:' : 'For next message:'}
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
