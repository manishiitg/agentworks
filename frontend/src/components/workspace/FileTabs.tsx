import { ChevronRight, X } from 'lucide-react'
import { useWorkspaceStore } from '../../stores/useWorkspaceStore'
import { openWorkspaceFile } from '../../utils/openWorkspaceFile'
import { FileTypeIcon } from './fileTypeIcon'

/** VS Code-style tabs for recently opened files in the shared viewer. */
export function FileTabs() {
  const openTabs = useWorkspaceStore(state => state.openTabs)
  const activePath = useWorkspaceStore(state => state.selectedFile?.path ?? '')
  if (openTabs.length === 0) return null

  const close = (path: string) => {
    const store = useWorkspaceStore.getState()
    const index = store.openTabs.findIndex(tab => tab.path === path)
    store.closeTab(path)
    if (path !== activePath) return
    const remaining = useWorkspaceStore.getState().openTabs
    const next = remaining[Math.min(index, remaining.length - 1)]
    if (next) void openWorkspaceFile(next.path)
    else store.setShowFileContent(false)
  }

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
            onClick={() => { if (!active) void openWorkspaceFile(tab.path) }}
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
  return start
}

/** Clickable path above the file; a folder reveals itself in the tree. */
export function FileBreadcrumbs({ path }: { path: string }) {
  const parts = path.split('/').filter(Boolean)
  const start = Math.min(displayStart(parts), parts.length - 1)
  const reveal = (index: number) => {
    const folder = parts.slice(0, index + 1).join('/')
    const store = useWorkspaceStore.getState()
    store.expandFoldersForFile(`${folder}/_`)
    void store.scrollToFile(folder)
  }
  return (
    <nav aria-label="File path" title={path} className="flex min-w-0 items-center gap-0.5 overflow-hidden text-xs text-muted-foreground">
      {parts.slice(start).map((part, offset) => {
        const index = start + offset
        const last = index === parts.length - 1
        return (
          <span key={index} className={`flex min-w-0 items-center gap-0.5 ${last ? 'shrink-0' : ''}`}>
            {offset > 0 && <ChevronRight aria-hidden="true" className="h-3 w-3 shrink-0 opacity-60" />}
            {last
              ? <span className="truncate text-foreground/80">{part}</span>
              : <button type="button" onClick={() => reveal(index)} className="truncate rounded px-0.5 hover:bg-muted hover:text-foreground">{part}</button>}
          </span>
        )
      })}
    </nav>
  )
}
