import type { ReactNode } from 'react'
import { Search, type LucideIcon } from 'lucide-react'
import { WorkspaceShortcutContents } from './WorkspaceShortcutContents'
import type { WorkspaceShortcut } from './workspaceShortcuts'
import type { ShortcutProduct } from '../../services/contextShortcuts'
export { WorkspaceContextShortcut } from './workspaceShortcuts'

export function WorkspaceContextCard({ children, summary, title = 'Project overview', product, legacyProduct, availableShortcuts, moreTools, consolidateConnections }: { children: ReactNode; summary?: string; title?: string; product?: ShortcutProduct; legacyProduct?: ShortcutProduct; availableShortcuts?: readonly WorkspaceShortcut[]; moreTools?: ReactNode; consolidateConnections?: boolean }) {
  return (
    <div className="p-3 sm:p-4">
      <aside
        aria-label="Workspace context"
        className="space-y-3 rounded-2xl border border-border bg-muted/70 p-4 shadow-sm dark:bg-zinc-800/80"
      >
        {product ? <WorkspaceShortcutContents product={product} legacy={legacyProduct} summary={summary} available={availableShortcuts} moreTools={moreTools} consolidateConnections={consolidateConnections}>{children}</WorkspaceShortcutContents> : <><header className="border-b border-border pb-3">
          <h2 className="text-sm font-semibold text-foreground">{title}</h2>
          {summary && <p className="mt-1 text-[11px] leading-4 text-muted-foreground">{summary}</p>}
        </header>
        {children}
        <button
          type="button"
          aria-label="All views"
          onClick={() =>
            window.dispatchEvent(new CustomEvent('open-quick-switcher', { detail: { query: '@panels ' } }))
          }
          className="flex w-full items-center gap-2.5 border-t border-border px-1 pt-3 text-xs text-muted-foreground hover:text-foreground"
        >
          <Search className="h-3.5 w-3.5" />
          <span className="flex-1 text-left">Find a panel</span>
          <kbd className="rounded border border-border px-1.5 py-0.5 text-[10px]">
            {typeof navigator !== 'undefined' && /Mac|iPhone|iPad/.test(navigator.platform) ? '⌘K' : 'Ctrl+K'}
          </kbd>
        </button>
        </>}
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
      className={`flex w-full items-center gap-2.5 rounded-md px-1 py-1.5 text-left text-xs ${attention ? 'bg-amber-500/10 text-amber-500' : active ? 'bg-background/50 text-foreground' : 'text-foreground/85'} hover:bg-background/40`}
    >
      <Icon className="h-3.5 w-3.5 shrink-0 text-muted-foreground" />
      <span className="min-w-0 flex-1 truncate">{children}</span>
      {detail && (
        <span className="max-w-[60%] shrink-0 truncate text-[10px] text-muted-foreground">{detail}</span>
      )}
    </button>
  )
}
