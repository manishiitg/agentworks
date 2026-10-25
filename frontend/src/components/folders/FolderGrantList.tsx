import { FolderOpen, Trash2 } from 'lucide-react'

export type FolderGrantItem = {
  id: string
  alias: string
  path: string
  access: 'read_only' | 'read_write'
  reason?: string
  available?: boolean
}

type FolderGrantListProps<T extends FolderGrantItem> = {
  grants: T[]
  disabled?: boolean
  emptyMessage?: string
  onRemove: (grant: T) => void | Promise<void>
  onAccessChange?: (grant: T, access: FolderGrantItem['access']) => void | Promise<void>
}

/** Shared presentation for owner-approved host-folder grants. */
export function FolderGrantList<T extends FolderGrantItem>({
  grants,
  disabled = false,
  emptyMessage = 'No external folders attached.',
  onRemove,
  onAccessChange,
}: FolderGrantListProps<T>) {
  if (grants.length === 0) {
    return <div className="rounded-lg border border-dashed border-border px-4 py-5 text-center text-xs text-muted-foreground">{emptyMessage}</div>
  }

  return (
    <div className="space-y-2">
      {grants.map((grant) => (
        <div key={grant.id} className="rounded-lg border border-border bg-muted/20 p-3">
          <div className="flex items-start gap-3">
            <FolderOpen className="mt-0.5 h-4 w-4 shrink-0 text-primary" />
            <div className="min-w-0 flex-1">
              <div className="flex flex-wrap items-center gap-2">
                <span className="text-sm font-medium text-foreground">{grant.alias}</span>

                {grant.available === false && (
                  <span className="rounded bg-destructive/10 px-1.5 py-0.5 text-[10px] text-destructive">Unavailable</span>
                )}
              </div>
              <div className="mt-1 truncate text-xs text-muted-foreground" title={grant.path}>{grant.path}</div>
              <details className="mt-1 text-xs text-muted-foreground"><summary className="cursor-pointer">Folder details</summary><code className="block pt-1">linked://{grant.alias}/</code>{grant.reason && <p className="pt-1">{grant.reason}</p>}</details>
            </div>
            {onAccessChange ? (
              <select
                value={grant.access}
                disabled={disabled}
                onChange={(event) => void onAccessChange(grant, event.target.value as FolderGrantItem['access'])}
                className="rounded-md border border-border bg-background px-2 py-1 text-xs text-foreground disabled:cursor-not-allowed disabled:opacity-50"
                aria-label={`Access for ${grant.alias}`}
              >
                <option value="read_only">Read only</option>
                <option value="read_write">Read &amp; write</option>
              </select>
            ) : (
              <span className="shrink-0 rounded bg-muted px-1.5 py-0.5 text-[10px] text-muted-foreground">
                {grant.access === 'read_write' ? 'Read + Write' : 'Read'}
              </span>
            )}
            <button
              type="button"
              disabled={disabled}
              onClick={() => void onRemove(grant)}
              className="shrink-0 rounded p-1.5 text-muted-foreground hover:bg-destructive/10 hover:text-destructive disabled:cursor-not-allowed disabled:opacity-50"
              aria-label={`Remove ${grant.alias}`}
            >
              <Trash2 className="h-3.5 w-3.5" />
            </button>
          </div>
        </div>
      ))}
    </div>
  )
}
