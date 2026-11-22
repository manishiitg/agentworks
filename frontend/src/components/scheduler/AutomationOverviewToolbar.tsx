import { RefreshCw, Search } from 'lucide-react'

export function AutomationOverviewToolbar({ label, search, onSearch, loading, onRefresh }: {
  label: 'schedules' | 'watchers' | 'webhooks'
  search: string
  onSearch: (value: string) => void
  loading: boolean
  onRefresh: () => void
}) {
  return <div className="mb-3 flex shrink-0 items-center gap-2">
    <div className="relative min-w-0 flex-1 sm:max-w-sm">
      <Search aria-hidden="true" className="absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-muted-foreground" />
      <input type="search" aria-label={`Search ${label}`} placeholder={`Search ${label}…`} value={search}
        onChange={event => onSearch(event.target.value)}
        className="h-9 w-full rounded-lg border border-border bg-card pl-9 pr-3 text-sm outline-none focus-visible:ring-2 focus-visible:ring-ring" />
    </div>
    <button type="button" title={`Refresh ${label}`} aria-label={`Refresh ${label}`} disabled={loading} onClick={onRefresh}
      className="grid h-9 w-9 shrink-0 place-items-center rounded-lg border border-border bg-card text-muted-foreground hover:bg-muted disabled:opacity-50">
      <RefreshCw aria-hidden="true" className={`h-4 w-4 ${loading ? 'animate-spin' : ''}`} />
    </button>
  </div>
}
