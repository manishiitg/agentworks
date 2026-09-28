import { AlertCircle, AlertTriangle, Loader2 } from 'lucide-react'
import { SettingsEmpty } from '../../components/ui/SettingsCard'
import { Button } from '../../components/ui/Button'

export function ConsoleLoading({ label }: { label: string }) {
  return (
    <p className="flex items-center gap-2 text-muted-foreground" role="status">
      <Loader2 className="h-4 w-4 animate-spin" aria-hidden />
      {label}
    </p>
  )
}

export function ConsoleError({ message, onRetry }: { message: string; onRetry: () => void }) {
  return (
    <div className="space-y-2 rounded border border-destructive/40 bg-destructive/5 p-3" role="alert">
      <p className="flex items-start gap-2 text-foreground">
        <AlertCircle className="mt-0.5 h-4 w-4 shrink-0 text-destructive" aria-hidden />
        {message}
      </p>
      <Button variant="outline" size="xs" onClick={onRetry}>
        Retry
      </Button>
    </div>
  )
}

/**
 * Shown when a refetch fails but previous data is still on screen: the old
 * content stays visible (preserving expanded rows and form state) with a
 * prominent warning instead of silently reading as current.
 */
export function ConsoleStale({ message, onRetry }: { message: string; onRetry: () => void }) {
  return (
    <div className="space-y-2 rounded border border-amber-500/40 bg-amber-500/5 p-3" role="alert">
      <p className="flex items-start gap-2 text-foreground">
        <AlertTriangle className="mt-0.5 h-4 w-4 shrink-0 text-amber-500" aria-hidden />
        Could not refresh — this data may be outdated. {message}
      </p>
      <Button variant="outline" size="xs" onClick={onRetry}>
        Retry
      </Button>
    </div>
  )
}

export function ConsoleEmpty({ children }: { children: string }) {
  return <SettingsEmpty>{children}</SettingsEmpty>
}
