import { useState, type ReactNode } from 'react'
import { ChevronDown, ChevronRight, X } from 'lucide-react'
import { useWorkspaceStore } from '../../stores/useWorkspaceStore'
import { closeOpenTab, openWorkspaceFile } from '../../utils/openWorkspaceFile'
import type { FileViewerSource } from './fileWorkspaceSource'
import { FileTypeIcon } from './fileTypeIcon'

/** VS Code's "Open Editors": the files opened in this view, one click to switch. */
export function OpenEditors({ source }: { source?: FileViewerSource }) {
  const workspaceTabs = useWorkspaceStore(state => state.openTabs)
  const openTabs = source?.openTabs ?? workspaceTabs
  const workspacePath = useWorkspaceStore(state => state.showFileContent ? state.selectedFile?.path ?? '' : '')
  const activePath = source ? source.showFileContent ? source.selectedFile?.path ?? '' : '' : workspacePath
  const [open, setOpen] = useState(true)
  return (
    <div className="border-b border-border">
      <button
        type="button"
        onClick={() => setOpen(value => !value)}
        aria-expanded={open}
        className="flex h-7 w-full items-center gap-1 px-2 text-left text-xs font-semibold uppercase tracking-wide text-muted-foreground hover:text-foreground"
      >
        {open ? <ChevronDown className="h-3.5 w-3.5 shrink-0" /> : <ChevronRight className="h-3.5 w-3.5 shrink-0" />}
        <span className="flex-1 truncate">Open Editors</span>
        {openTabs.length > 0 && <span className="rounded-full bg-muted px-1.5 text-[10px] font-medium normal-case leading-4 text-foreground">{openTabs.length}</span>}
      </button>
      {open && (
        <ul className="pb-1">
          {openTabs.length === 0 && <li className="px-4 py-1 text-xs text-muted-foreground">No files open.</li>}
          {openTabs.map(tab => {
            const active = tab.path === activePath
            const dir = tab.path.split('/').slice(0, -1).slice(-1)[0] ?? ''
            return (
              <li key={tab.path} className={`group/open flex h-7 items-center gap-1.5 pl-5 pr-2 text-[13px] ${active ? 'bg-primary/15' : 'hover:bg-muted'}`}>
                <button type="button" title={tab.path} onClick={() => { if (!active) void (source ? source.openFile(tab.path) : openWorkspaceFile(tab.path)) }} className="flex min-w-0 flex-1 items-center gap-1.5 text-left">
                  <FileTypeIcon name={tab.name} />
                  <span className="truncate text-foreground">{tab.name}</span>
                  {dir && <span className="truncate text-xs text-muted-foreground">{dir}</span>}
                </button>
                <button type="button" aria-label={`Close ${tab.name}`} onClick={() => source ? source.closeFile(tab.path) : closeOpenTab(tab.path, activePath)} className="rounded p-0.5 text-muted-foreground opacity-0 hover:bg-background hover:text-foreground focus-visible:opacity-100 group-hover/open:opacity-100">
                  <X className="h-3.5 w-3.5" />
                </button>
              </li>
            )
          })}
        </ul>
      )}
    </div>
  )
}

/** The EXPLORER title, Open Editors, and the folder row that carries the toolbar icons. */
export function ExplorerHeader({ title, titleAction, toolbar, leading, source }: {
  title: string
  source?: FileViewerSource
  /** Right of the EXPLORER title (Ask AI). */
  titleAction?: ReactNode
  toolbar: ReactNode
  /** Left of the folder name (select-all in selection mode). */
  leading?: ReactNode
}) {
  return (
    <div className="shrink-0 border-b border-border">
      <div className="flex h-9 items-center justify-between gap-2 px-3">
        <h2 className="text-xs font-semibold uppercase tracking-wide text-muted-foreground">Explorer</h2>
        <div className="flex items-center gap-1">{titleAction}</div>
      </div>
      <OpenEditors source={source} />
      <div className="flex h-8 items-center gap-1 px-2">
        {leading}
        <span className="min-w-0 flex-1 truncate text-xs font-semibold uppercase tracking-wide text-foreground" title={title}>{title}</span>
        <div className="flex shrink-0 items-center gap-0.5">{toolbar}</div>
      </div>
    </div>
  )
}
