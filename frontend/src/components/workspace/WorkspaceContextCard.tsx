import type { ReactNode } from 'react'
import { ArrowLeft, ChevronRight, List, Search, type LucideIcon } from 'lucide-react'
export function WorkspaceContextCard({ children, title, summary, subtle = false, onFullMenu, onBack }: { children: ReactNode; title?: string; summary?: string; subtle?: boolean; onFullMenu?: () => void; onBack?: () => void }) {
  return (
    <div className="flex h-full min-h-0 flex-col p-3 sm:p-4">
      <aside
        aria-label="Workspace context"
        className={`flex max-h-full min-h-0 flex-col gap-3 overflow-hidden rounded-2xl border border-border p-4 shadow-sm ${subtle ? 'bg-muted/30 dark:bg-zinc-900/70' : 'bg-muted/70 dark:bg-zinc-800/80'}`}
      >
        {(title || summary) && <header className="shrink-0 border-b border-border pb-3">
          {title && <h2 className="text-sm font-semibold text-foreground">{onBack ? <button type="button" aria-label="Back to workspace" title="Back to workspace" onClick={onBack} className="inline-flex min-h-8 items-center gap-2 rounded hover:text-foreground/80 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"><ArrowLeft aria-hidden="true" className="h-3.5 w-3.5"/>{title}</button> : title}</h2>}
          {summary && <p className="mt-1 text-[11px] leading-4 text-muted-foreground">{summary}</p>}
        </header>}
        <div className="workspace-menu-scroll min-h-0 flex-1 space-y-3 overflow-y-auto overscroll-contain">{children}</div>
        <footer className="flex shrink-0 items-center gap-3 border-t border-border pt-3">
        <button
          type="button"
          aria-label="All tools"
          onClick={() =>
            window.dispatchEvent(new CustomEvent('open-quick-switcher', { detail: { query: '@panels ' } }))
          }
          className="flex min-h-8 min-w-0 flex-1 items-center gap-1.5 rounded px-1 text-xs text-muted-foreground hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
        >
          <Search className="h-3.5 w-3.5 shrink-0" />
          <span className="flex-1 whitespace-nowrap text-left">All tools</span>
          <kbd className="shrink-0 rounded border border-border px-1.5 py-0.5 text-[10px]">
            {typeof navigator !== 'undefined' && /Mac|iPhone|iPad/.test(navigator.platform) ? '⌘K' : 'Ctrl+K'}
          </kbd>
        </button>
        {onFullMenu && <button type="button" aria-label="Full menu" onClick={onFullMenu} className="flex min-h-8 shrink-0 items-center gap-1.5 rounded px-1 text-xs text-muted-foreground hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring">
          <List className="h-3.5 w-3.5 shrink-0"/><span className="whitespace-nowrap text-left">Full menu</span><ChevronRight className="h-3 w-3"/>
        </button>}
        </footer>
      </aside>
    </div>
  )
}
export function WorkspaceContextSection({
  title,
  action,
  children,
}: {
  title: string
  action?: ReactNode
  children: ReactNode
}) {
  return (
    <section>
      <div className="mb-1.5 flex items-center justify-between gap-2">
        <h2 className="text-[11px] font-medium text-muted-foreground">{title}</h2>
        {action}
      </div>
      {children}
    </section>
  )
}

/** Secondary destinations remain reachable without crowding everyday work. */
export function WorkspaceContextGroup({ title, collapsed = false, defaultOpen = false, children }: { title: string; collapsed?: boolean; defaultOpen?: boolean; children: ReactNode }) {
  if (!collapsed) return <WorkspaceContextSection title={title}>{children}</WorkspaceContextSection>
  return <details open={defaultOpen || undefined} className="group/ops border-t border-border pt-2">
    <summary className="flex cursor-pointer list-none items-center gap-1.5 py-1 text-[11px] font-medium text-muted-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring [&::-webkit-details-marker]:hidden">
      <ChevronRight aria-hidden="true" className="h-3 w-3 transition-transform group-open/ops:rotate-90" />{title}
    </summary>
    <div className="mt-1 space-y-1">{children}</div>
  </details>
}
export function WorkspaceContextRow({
  icon: Icon,
  children,
  detail,
  onClick,
  hint,
  attention = false,
  active = false,
}: {
  icon: LucideIcon
  children: ReactNode
  detail?: ReactNode
  onClick: () => void
  hint?: string
  attention?: boolean
  active?: boolean
}) {
  return (
    <button
      type="button"
      onClick={onClick}
      title={hint}
      aria-current={active ? 'page' : undefined}
      className={`flex w-full items-center gap-2.5 rounded-md px-1 min-h-8 py-1.5 text-left text-xs ${attention ? 'bg-amber-500/10 text-amber-500' : active ? 'bg-background/50 text-foreground' : 'text-foreground/85'} hover:bg-background/40 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring`}
    >
      <Icon className="h-3.5 w-3.5 shrink-0 text-muted-foreground" />
      <span className="min-w-0 flex-1 truncate">{children}</span>
      {detail && (
        <span className="max-w-[60%] shrink-0 truncate text-[10px] text-muted-foreground">{detail}</span>
      )}
    </button>
  )
}
