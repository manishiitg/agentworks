import { LoadingIndicator } from '../ui/LoadingIndicator'
import { ChatComposerAttachmentTiles } from './ChatComposer'

/** Immediate local preview only; a file becomes an attachment after the server accepts it. */
export function UploadingAttachments({ files }: { files: File[] }) {
  return <div className="px-2 pt-2 pb-1" aria-busy="true">
    <div role="status" className="mb-1 flex items-center gap-1.5 text-xs text-muted-foreground">
      <LoadingIndicator aria-hidden="true" className="h-3.5 w-3.5 shrink-0" />
      <span className="min-w-0 truncate">{files.length === 1 ? `Uploading ${files[0].name}…` : `Uploading ${files.length} files…`}</span>
    </div>
    <ChatComposerAttachmentTiles files={files} dim />
  </div>
}
