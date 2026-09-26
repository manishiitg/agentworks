import { useCallback, useEffect, useState } from 'react'
import { GatewayApiError } from './gatewayAdminApi'

export function gatewayErrorMessage(err: unknown): string {
  if (err instanceof GatewayApiError) return err.message
  if (err instanceof Error) return err.message
  return 'Something went wrong.'
}

export const tableClass = 'w-full border-collapse text-xs'
export const thClass = 'border-b border-border px-2 py-1.5 text-left font-medium text-muted-foreground'
export const tdClass = 'border-b border-border/60 px-2 py-1.5 align-top'
export const codeClass = 'rounded bg-muted px-1 py-0.5 font-mono text-[11px]'

/**
 * Loads console data once per attempt with cancellation. Panels refetch by
 * bumping the attempt counter (manual refresh only — no polling, no retry
 * storms against a gateway that may be down).
 */
export function useGatewayLoader<T>(load: () => Promise<T>, attempt: number): {
  data: T | null
  loading: boolean
  error: string | null
} {
  const [data, setData] = useState<T | null>(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)

  useEffect(() => {
    let cancelled = false
    setLoading(true)
    setError(null)
    void load().then(
      (value) => {
        if (cancelled) return
        setData(value)
        setLoading(false)
      },
      (err: unknown) => {
        if (cancelled) return
        setError(gatewayErrorMessage(err))
        setLoading(false)
      },
    )
    return () => {
      cancelled = true
    }
    // load is panel-local and stable per attempt; attempt drives refetch.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [attempt])

  return { data, loading, error }
}

export function useAttempt(): [number, () => void] {
  const [attempt, setAttempt] = useState(0)
  const bump = useCallback(() => setAttempt((n) => n + 1), [])
  return [attempt, bump]
}
