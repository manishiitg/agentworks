import { useMemo, useRef, useState, type ReactNode } from 'react'
import { RefreshCw, Search } from 'lucide-react'
import type { PlannerFile } from '../../services/api-types'
import { ExplorerHeader } from './ExplorerHeader'
import PlannerFileList from './PlannerFileList'
import type { ReadOnlyFileWorkspaceSource } from './fileWorkspaceSource'

export function ReadOnlyFileTree({ source, title, headerAction }: { source: ReadOnlyFileWorkspaceSource; title: string; headerAction?: ReactNode }) {
  const [query, setQuery] = useState('')
  const scrollRef = useRef<HTMLDivElement>(null)
  const files = useMemo(() => {
    const match = query.trim().toLowerCase()
    if (!match) return source.files
    const filter = (items: PlannerFile[]): PlannerFile[] => items.flatMap(file => {
      const children = filter(file.children || [])
      return file.filepath.toLowerCase().includes(match) ? [file] : children.length ? [{ ...file, children }] : []
    })
    return filter(source.files)
  }, [source.files, query])
  return <div className="flex h-full min-h-0 flex-col">
    <ExplorerHeader title={title} titleAction={headerAction} source={source} toolbar={<button type="button" onClick={source.refresh} aria-label="Refresh files" className="rounded p-1 text-muted-foreground hover:bg-muted"><RefreshCw className="h-3.5 w-3.5" /></button>} />
    <label className="relative mx-3 my-2"><Search className="absolute left-2 top-2 h-3.5 w-3.5 text-muted-foreground" /><input aria-label="Search files" placeholder="Search files…" value={query} onChange={event => setQuery(event.target.value)} className="w-full rounded-md border border-border bg-background py-1.5 pl-7 pr-2 text-xs" /></label>
    {source.error && source.files.length > 0 && <p role="alert" className="mx-3 mb-2 text-xs text-destructive">{source.error}</p>}
    <div ref={scrollRef} className="min-h-0 flex-1 overflow-y-auto px-1">
      <PlannerFileList files={files} loading={source.loading} error={source.error} expandedFolders={source.expandedFolders} forceExpandFolders={!!query.trim()} selectedPath={source.selectedFile?.path} readOnly hideAddToChat hideRootActions scrollContainerRef={scrollRef}
        onFolderClick={folder => source.toggleFolder(folder.filepath)} onFileClick={file => { void source.openFile(file.filepath) }} onRetry={source.refresh}
        onFileDelete={() => {}} onFolderDelete={() => {}} chatFileContext={[]} addFileToContext={() => {}} />
    </div>
  </div>
}
