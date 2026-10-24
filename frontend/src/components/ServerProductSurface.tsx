import { useEffect, useState } from 'react'
import { getAuthToken } from '../services/api'
import { useAuthStore } from '../stores/useAuthStore'
import { ModePresetBar } from './ModePresetBar'

/** Public shell for reviewed first-party code. Same-origin session/API access
 * is intentional; sandbox/CSP restrict browser capabilities, not account scope. */
export function ServerProductSurface({ product, label }: { product: 'llm-gateway'; label: string }) {
  const [ready, setReady] = useState(false)
  const [error, setError] = useState('')
  const userID = useAuthStore(state => state.user?.id)
  const prefix = `/api/server-products/${product}`
  useEffect(() => {
    const controller = new AbortController()
    setReady(false); setError('')
    let timer: ReturnType<typeof setTimeout> | undefined
    const refresh = async () => {
      try {
        const token = getAuthToken()
        if (!token) throw new Error('Sign in to view ' + label + '.')
        const response = await fetch(`${prefix}/session`, {
          method: 'POST', credentials: 'same-origin', cache: 'no-store', signal: controller.signal,
          headers: { Authorization: `Bearer ${token}`, 'X-AgentWorks-Product-Session': '1' },
        })
        if (!response.ok) throw new Error(`${label} is unavailable for this account or is not installed on this server.`)
        if (controller.signal.aborted) return
        setReady(true)
        timer = setTimeout(() => { void refresh() }, 4 * 60 * 1000)
      } catch (err) {
        if (!controller.signal.aborted) {
          setReady(false)
          setError(err instanceof Error ? err.message : 'Server product unavailable')
        }
      }
    }
    void refresh()
    return () => { controller.abort(); clearTimeout(timer) }
  }, [prefix, label, userID])
  return <div className="flex h-screen min-h-0 bg-background">
    <ModePresetBar reduced />
    <main className="min-w-0 flex-1" aria-label={label}>
      {error ? <p role="alert" className="p-6 text-muted-foreground">{error}</p>
        : ready ? <iframe title={label} src={`${prefix}/ui/`} sandbox="allow-scripts allow-same-origin" referrerPolicy="no-referrer" className="h-full w-full border-0" />
          : <p role="status" className="p-6 text-muted-foreground">Loading {label}…</p>}
    </main>
  </div>
}
