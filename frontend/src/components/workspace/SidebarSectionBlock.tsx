import { useEffect, useLayoutEffect, useRef, useState, type ReactNode } from 'react'
import { createPortal } from 'react-dom'
import { ArrowDown, ArrowUp, ChevronRight, Layers, MoreHorizontal, Pencil, Trash2 } from 'lucide-react'

const PROJECT_DRAG_MIME = 'text/x-agentworks-project'

export function readDraggedProjectKey(event: React.DragEvent): string {
  return event.dataTransfer.getData(PROJECT_DRAG_MIME) || ''
}

export function hasDraggedProject(event: React.DragEvent): boolean {
  return Array.from(event.dataTransfer.types || []).includes(PROJECT_DRAG_MIME)
}

interface SidebarSectionBlockProps {
  id: string
  name: string
  count: number
  collapsed: boolean
  activityState?: 'running' | 'waiting'
  canMoveUp: boolean
  canMoveDown: boolean
  onToggle: () => void
  onRename: (nextName: string) => void
  onMoveUp: () => void
  onMoveDown: () => void
  onDelete: () => void
  onDropProject: (projectKey: string) => void
  children: ReactNode
}

/** Personal collapsible sidebar section for grouping projects without cluttering the menu. */
export function SidebarSectionBlock({ id, name, count, collapsed, activityState, canMoveUp, canMoveDown, onToggle, onRename, onMoveUp, onMoveDown, onDelete, onDropProject, children }: SidebarSectionBlockProps) {
  const [dragOver, setDragOver] = useState(false)
  const [renaming, setRenaming] = useState(false)
  const [draft, setDraft] = useState(name)
  const trigger = useRef<HTMLButtonElement>(null)
  const panel = useRef<HTMLDivElement>(null)
  const [menuPos, setMenuPos] = useState<{ top: number; left: number } | null>(null)

  useLayoutEffect(() => {
    if (!menuPos) return
    const rect = trigger.current?.getBoundingClientRect()
    const height = panel.current?.getBoundingClientRect().height || 0
    if (!rect) return
    const top = Math.max(8, rect.bottom + 4 + height <= window.innerHeight - 8 ? rect.bottom + 4 : rect.top - height - 4)
    const left = Math.max(8, Math.min(rect.right - 176, window.innerWidth - 184))
    setMenuPos(prev => prev?.top === top && prev.left === left ? prev : { top, left })
  }, [menuPos])

  useEffect(() => {
    if (!menuPos) return
    const outside = (event: MouseEvent) => {
      const target = event.target as Node
      if (!panel.current?.contains(target) && !trigger.current?.contains(target)) setMenuPos(null)
    }
    const escape = (event: KeyboardEvent) => {
      if (event.key === 'Escape') { event.preventDefault(); setMenuPos(null); trigger.current?.focus() }
    }
    const dismiss = () => setMenuPos(null)
    document.addEventListener('mousedown', outside)
    document.addEventListener('keydown', escape)
    window.addEventListener('resize', dismiss)
    return () => {
      document.removeEventListener('mousedown', outside)
      document.removeEventListener('keydown', escape)
      window.removeEventListener('resize', dismiss)
    }
  }, [menuPos])

  const commitRename = () => {
    const trimmed = draft.trim()
    if (trimmed && trimmed !== name) onRename(trimmed)
    setRenaming(false)
  }

  return <section
    aria-label={`Section ${name}`}
    data-sidebar-section={id}
    onDragOver={event => {
      if (!hasDraggedProject(event)) return
      event.preventDefault()
      event.dataTransfer.dropEffect = 'move'
      if (!dragOver) setDragOver(true)
    }}
    onDragLeave={event => {
      if (!event.currentTarget.contains(event.relatedTarget as Node | null)) setDragOver(false)
    }}
    onDrop={event => {
      const key = readDraggedProjectKey(event)
      setDragOver(false)
      if (!key) return
      event.preventDefault()
      onDropProject(key)
    }}
    className={`mb-2 shrink-0 rounded-lg border transition-colors ${dragOver ? 'border-primary/60 bg-primary/10' : 'border-transparent'}`}
  >
    {renaming ? (
      <form
        className="flex items-center gap-1 px-1 py-1"
        onSubmit={event => { event.preventDefault(); commitRename() }}
      >
        <input
          type="text"
          aria-label="Rename section"
          autoFocus
          value={draft}
          onChange={event => setDraft(event.target.value)}
          onBlur={commitRename}
          onKeyDown={event => { if (event.key === 'Escape') { event.preventDefault(); setDraft(name); setRenaming(false) } }}
          className="h-6 min-w-0 flex-1 rounded-md border border-primary/50 bg-background px-2 text-xs text-foreground focus:outline-none focus:ring-2 focus:ring-ring"
        />
      </form>
    ) : (
      <div className="group/section flex h-7 max-md:h-9 items-center gap-1 rounded-md px-1 hover:bg-muted/70">
        <button
          type="button"
          aria-expanded={!collapsed}
          onClick={onToggle}
          className="flex h-full min-w-0 flex-1 items-center gap-1.5 rounded-md px-1 text-left text-xs font-medium text-muted-foreground hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
        >
          <ChevronRight aria-hidden="true" className={`h-3 w-3 shrink-0 transition-transform ${collapsed ? '' : 'rotate-90'}`} />
          <Layers aria-hidden="true" className="h-3 w-3 shrink-0 text-muted-foreground/75" />
          <span className="min-w-0 truncate">{name}</span>
          {collapsed && count > 0 && <span className="shrink-0 rounded-full bg-muted px-1.5 text-[10px] leading-4 text-muted-foreground tabular-nums">{count}</span>}
          {collapsed && activityState === 'running' && <span role="img" aria-label="Working" title="Agent working in this section" className="h-1.5 w-1.5 shrink-0 rounded-full bg-info motion-safe:animate-pulse" />}
          {collapsed && activityState === 'waiting' && <span role="status" aria-label="Waiting" title="Agent waiting in this section" className="h-1.5 w-1.5 shrink-0 rounded-full bg-[hsl(var(--warning))]" />}
        </button>
        <button
          ref={trigger}
          type="button"
          aria-label={`Section actions for ${name}`}
          aria-haspopup="dialog"
          aria-expanded={Boolean(menuPos)}
          onClick={() => {
            if (menuPos) { setMenuPos(null); return }
            const rect = trigger.current?.getBoundingClientRect()
            if (rect) setMenuPos({ top: rect.bottom + 4, left: Math.max(8, Math.min(rect.right - 176, window.innerWidth - 184)) })
          }}
          className="flex h-6 w-6 shrink-0 items-center justify-center rounded-md text-muted-foreground hover:bg-muted hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring md:opacity-0 md:group-hover/section:opacity-100 md:group-focus-within/section:opacity-100"
        >
          <MoreHorizontal aria-hidden="true" className="h-3.5 w-3.5" />
        </button>
      </div>
    )}

    {!collapsed && (
      <div className="space-y-1 pt-0.5">
        {count > 0 ? children : <p className="px-5 py-1 text-[11px] italic text-muted-foreground/75">Drag projects here or use ⋯</p>}
      </div>
    )}

    {menuPos && createPortal(
      <div
        ref={panel}
        role="dialog"
        aria-label={`Section actions for ${name}`}
        style={menuPos}
        className="fixed z-[100] w-44 rounded-lg border border-border bg-popover p-1 text-popover-foreground shadow-lg"
      >
        <button type="button" onClick={() => { setMenuPos(null); setDraft(name); setRenaming(true) }} className="flex w-full items-center gap-2 rounded-md px-2 py-1.5 text-left text-xs hover:bg-muted focus-visible:ring-2 focus-visible:ring-ring">
          <Pencil aria-hidden="true" className="h-3.5 w-3.5 text-muted-foreground" />Rename section
        </button>
        {canMoveUp && <button type="button" onClick={() => { setMenuPos(null); onMoveUp() }} className="flex w-full items-center gap-2 rounded-md px-2 py-1.5 text-left text-xs hover:bg-muted focus-visible:ring-2 focus-visible:ring-ring">
          <ArrowUp aria-hidden="true" className="h-3.5 w-3.5 text-muted-foreground" />Move up
        </button>}
        {canMoveDown && <button type="button" onClick={() => { setMenuPos(null); onMoveDown() }} className="flex w-full items-center gap-2 rounded-md px-2 py-1.5 text-left text-xs hover:bg-muted focus-visible:ring-2 focus-visible:ring-ring">
          <ArrowDown aria-hidden="true" className="h-3.5 w-3.5 text-muted-foreground" />Move down
        </button>}
        <div className="my-1 border-t border-border" />
        <button type="button" onClick={() => { setMenuPos(null); onDelete() }} className="flex w-full items-center gap-2 rounded-md px-2 py-1.5 text-left text-xs text-destructive hover:bg-destructive/10 focus-visible:ring-2 focus-visible:ring-ring">
          <Trash2 aria-hidden="true" className="h-3.5 w-3.5" />Delete section
        </button>
      </div>,
      document.body,
    )}
  </section>
}
