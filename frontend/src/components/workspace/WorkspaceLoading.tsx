import { usePersistentTab } from '../../hooks/usePersistentTab'

/** Empty layout only: no saved project or account content before admission. */
export function WorkspaceLoading() {
  const [navigation] = usePersistentTab('product_navigation_mode', 'fixed', ['fixed', 'auto-hide'] as const)
  return <div role="status" aria-label="Loading workspace" className="flex h-dvh overflow-hidden bg-background text-foreground">
    <div aria-hidden="true" className={`hidden shrink-0 border-r border-border bg-muted/20 p-3 md:block ${navigation === 'fixed' ? 'w-[260px]' : 'w-12'}`}>
      <div className="h-6 rounded bg-muted" />
      {navigation === 'fixed' && <div className="mt-10 space-y-5">{[0, 1, 2, 3, 4].map(row => <div key={row} className="h-5 rounded bg-muted" />)}</div>}
    </div>
    <div className="flex min-w-0 flex-1 flex-col">
      <div aria-hidden="true" className="flex h-[50px] shrink-0 items-center gap-3 border-b border-border px-5">
        {[0, 1, 2].map(tab => <div key={tab} className="h-6 w-24 rounded bg-muted" />)}
      </div>
      <div className="flex flex-1 items-center justify-center text-sm text-muted-foreground">Loading workspace…</div>
      <div aria-hidden="true" className="mx-5 mb-5 h-24 rounded-xl border border-border" />
    </div>
  </div>
}
