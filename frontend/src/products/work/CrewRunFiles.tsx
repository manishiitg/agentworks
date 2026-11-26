import { useEffect, useState } from 'react'
import { crewRunFilesApi, type CrewRunFileItem } from '../../api/crewRunFiles'
import { MarkdownRenderer } from '../../components/ui/MarkdownRenderer'

/** A size people can read: 812 B, 3.4 KB, 1.2 MB. */
export function fileSizeLabel(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`
  return `${(bytes / (1024 * 1024)).toFixed(1)} MB`
}

/** Links to the files the run made, such as a report. Nothing shows when it made none or the list cannot be read. */
export function CrewRunFiles({ workspacePath, runFolder }: { workspacePath: string; runFolder: string }) {
  const [files, setFiles] = useState<CrewRunFileItem[]>([])
  useEffect(() => {
    let current = true
    setFiles([])
    crewRunFilesApi.get(workspacePath, runFolder)
      .then(list => { if (current) setFiles(list) })
      .catch(() => { /* Cannot list the files: show nothing rather than guess. */ })
    return () => { current = false }
  }, [workspacePath, runFolder])
  if (!files.length) return null
  return <div className="space-y-1 px-4 py-2" data-testid="run-files">
    <p className="text-xs font-semibold">Files this run made</p>
    {files.map(file => <div key={file.path} className="flex items-baseline justify-between gap-3">
      <MarkdownRenderer content={`[${file.name}](#workspace/${encodeURIComponent(file.path)})`} />
      <span className="shrink-0 text-xs text-muted-foreground">{fileSizeLabel(file.size)}</span>
    </div>)}
  </div>
}
