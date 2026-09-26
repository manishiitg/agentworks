import { AlertCircle, Loader2 } from 'lucide-react'
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

export function ConsoleEmpty({ children }: { children: string }) {
  return <SettingsEmpty>{children}</SettingsEmpty>
}
