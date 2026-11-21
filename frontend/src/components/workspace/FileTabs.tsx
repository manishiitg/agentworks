import { ChevronRight, X } from 'lucide-react'
import { useWorkspaceStore } from '../../stores/useWorkspaceStore'
import { closeOpenTab, openWorkspaceFile } from '../../utils/openWorkspaceFile'
import type { FileViewerSource } from './fileWorkspaceSource'
import { FileTypeIcon } from './fileTypeIcon'

/** VS Code-style tabs for recently opened files in the shared viewer. */
export function FileTabs({ source }: { source?: FileViewerSource }) {
  const workspaceTabs = useWorkspaceStore(state => state.openTabs)
  const openTabs = source?.openTabs ?? workspaceTabs
  const workspacePath = useWorkspaceStore(state => state.selectedFile?.path ?? '')
  const activePath = source ? source.selectedFile?.path ?? '' : workspacePath
  // One open file is already named in the header; the tab strip only helps once there are several (owner, 20 Nov).
  if (openTabs.length < 2) return null

  const close = (path: string) => source ? source.closeFile(path) : closeOpenTab(path, activePath)

  return (
    <div role="tablist" aria-label="Open files" className="flex shrink-0 overflow-x-auto border-b border-border bg-muted/30 [scrollbar-width:thin]">
      {openTabs.map(tab => {
        const active = tab.path === activePath
        return (
          <div
            key={tab.path}
            role="tab"
            aria-selected={active}
            title={tab.path}
            onClick={() => { if (!active) void (source ? source.openFile(tab.path) : openWorkspaceFile(tab.path)) }}
            onAuxClick={event => { if (event.button === 1) { event.preventDefault(); close(tab.path) } }}
            className={`group/tab flex h-8 max-w-[14rem] shrink-0 cursor-pointer items-center gap-1.5 border-r border-border pl-3 pr-1.5 text-[13px] ${
              active
                ? 'bg-background text-foreground shadow-[inset_0_2px_0_hsl(var(--primary))]'
                : 'text-muted-foreground hover:bg-background/60 hover:text-foreground'
            }`}
          >
            <FileTypeIcon name={tab.name} />
            <span className="truncate">{tab.name}</span>
            <button
              type="button"
              aria-label={`Close ${tab.name}`}
              onClick={event => { event.stopPropagation(); close(tab.path) }}
              className={`rounded p-0.5 hover:bg-muted ${active ? 'opacity-100' : 'opacity-0 group-hover/tab:opacity-100'} focus-visible:opacity-100`}
            >
              <X className="h-3.5 w-3.5" />
            </button>
          </div>
        )
      })}
    </div>
  )
}

// The part of a path worth showing: drop the per-user and product prefixes
// ("_users/<id>/", "Chats/<Product>/projects/").
function displayStart(parts: string[]): number {
  let start = 0
  if (parts[0] === '_users' && parts.length > 2) start = 2
  if (parts[start] === 'Chats' && parts[start + 2] === 'projects') start += 3
  // A Crew's own folder name and its _files segment say nothing the pane does not already show.
  if (parts[start] === 'Crew' && parts.length > start + 2) start += 2
  if (parts[start] === '_files' && parts.length > start + 1) start += 1
  return start
}

/** The file's path on one line, like a code editor's breadcrumbs: folders muted and clickable (a folder reveals itself
 * in the tree), the file name last and bold. A deep path keeps its first folder and the last two; the middle collapses
 * to "…" and the whole path shows on hover. */
export function FileBreadcrumbs({ path, onReveal }: { path: string; onReveal?: (path: string) => void }) {
  const parts = path.split('/').filter(Boolean)
  const start = Math.min(displayStart(parts), parts.length - 1)
  const reveal = (index: number) => {
    const folder = parts.slice(0, index + 1).join('/')
    if (onReveal) { onReveal(folder); return }
    const store = useWorkspaceStore.getState()
    store.expandFoldersForFile(`${folder}/_`)
    void store.scrollToFile(folder)
  }
  const last = parts.length - 1
  const folders = Array.from({ length: Math.max(0, last - start) }, (_, offset) => start + offset)
  const shown: Array<number | 'gap'> = folders.length > 3 ? [folders[0], 'gap', ...folders.slice(-2)] : folders
  const sep = <ChevronRight aria-hidden="true" className="h-3 w-3 shrink-0 opacity-50" />
  return (
    <nav aria-label="File path" title={parts.slice(start).join('/')} className="flex min-w-0 items-center gap-0.5 overflow-hidden text-xs font-normal text-muted-foreground">
      {shown.map(item => item === 'gap'
        ? <span key="gap" className="flex shrink-0 items-center gap-0.5"><span aria-label="more folders">…</span>{sep}</span>
        : <span key={item} className="flex min-w-0 items-center gap-0.5">
            <button type="button" onClick={() => reveal(item)} className="max-w-[9rem] truncate rounded px-0.5 hover:bg-muted hover:text-foreground">{parts[item]}</button>{sep}
          </span>)}
      <span className="min-w-0 shrink-0 truncate text-sm font-semibold text-foreground">{parts[last]}</span>
    </nav>
  )
}
