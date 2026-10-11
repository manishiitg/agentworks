import { useEffect, useState } from 'react'
import { ModePresetBar } from './ModePresetBar'

/** Public shell for reviewed first-party code. Same-origin session/API access
 * is intentional; sandbox/CSP restrict browser capabilities, not account scope. */
export function ServerProductSurface({ product, label }: { product: 'llm-gateway'; label: string }) {
  const [ready, setReady] = useState(false)
  const [error, setError] = useState('')
  const prefix = `/api/server-products/${product}`
  useEffect(() => {
    const controller = new AbortController()
    setReady(false); setError('')
    fetch(`${prefix}/me`, { credentials: 'same-origin', cache: 'no-store', signal: controller.signal })
      .then(async response => { if (!response.ok) throw new Error(`${label} is unavailable for this account or is not installed on this server.`); await response.json(); setReady(true) })
      .catch(err => { if (!controller.signal.aborted) setError(err instanceof Error ? err.message : 'Server product unavailable') })
    return () => controller.abort()
  }, [prefix, label])
  return <div className="flex h-screen min-h-0 bg-background">
    <ModePresetBar reduced walkthroughPaused />
    <main className="min-w-0 flex-1" aria-label={label}>
      {error ? <p role="alert" className="p-6 text-muted-foreground">{error}</p>
        : ready ? <iframe title={label} src={`${prefix}/ui/`} sandbox="allow-scripts allow-same-origin" referrerPolicy="no-referrer" className="h-full w-full border-0" />
          : <p role="status" className="p-6 text-muted-foreground">Loading {label}…</p>}
    </main>
  </div>
}
