import { AlertCircle, AlertTriangle, Loader2 } from 'lucide-react'
import { SettingsEmpty } from '../../components/ui/SettingsCard'
import { Button } from '../../components/ui/Button'
import { createContext, useCallback, useContext, useEffect, useId, useRef, useState, type ReactNode } from 'react'

type Failure = { message: string; stale: boolean; getRetry: () => () => void }
const FailureContext = createContext<((id: string, failure: Failure) => () => void) | null>(null)

/** Nested sections share a single recovery action, including after a gateway restart. */
export function GatewayFeedbackBoundary({ children }: { children: ReactNode }) {
  const [failures, setFailures] = useState<Map<string, Failure>>(() => new Map())
  const register = useCallback((id: string, failure: Failure) => {
    setFailures(previous => new Map(previous).set(id, failure))
    return () => setFailures(previous => {
      if (!previous.has(id)) return previous
      const next = new Map(previous)
      next.delete(id)
      return next
    })
  }, [])
  const messages = [...new Set([...failures.values()].map(failure => failure.message))]
  return <FailureContext.Provider value={register}>
    {messages.length > 0 && <div className="space-y-2 rounded border border-destructive/40 bg-destructive/5 p-3" role="alert">
      <p className="flex items-center gap-2 font-medium text-foreground"><AlertCircle className="h-4 w-4 shrink-0 text-destructive" aria-hidden />Could not refresh Vault</p>
      {[...failures.values()].some(failure => failure.stale) && <p className="text-muted-foreground">Previously loaded data is still shown and may be outdated.</p>}
      {messages.map(message => <p key={message} className="text-muted-foreground">{message}</p>)}
      <Button variant="outline" size="xs" onClick={() => {
        // Several child sections use the same parent refresh callback.
        for (const retry of new Set([...failures.values()].map(failure => failure.getRetry()))) retry()
      }}>Retry</Button>
    </div>}
    {children}
  </FailureContext.Provider>
}

function useSharedFailure(message: string, stale: boolean, onRetry: () => void): boolean {
  const register = useContext(FailureContext)
  const id = useId()
  const retryRef = useRef(onRetry)
  retryRef.current = onRetry
  useEffect(() => register?.(id, { message, stale, getRetry: () => retryRef.current }), [register, id, message, stale])
  return register !== null
}

export function ConsoleLoading({ label }: { label: string }) {
  return (
    <p className="flex items-center gap-2 text-muted-foreground" role="status">
      <Loader2 className="h-4 w-4 animate-spin" aria-hidden />
      {label}
    </p>
  )
}

export function ConsoleError({ message, onRetry }: { message: string; onRetry: () => void }) {
  if (useSharedFailure(message, false, onRetry)) return null
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
  if (useSharedFailure(message, true, onRetry)) return null
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
