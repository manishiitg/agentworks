import { useEffect, useRef, useState } from 'react'
import api from '../services/api'
import { useAuthStore } from '../stores/useAuthStore'

export function AgentServerLinkReturn() {
  const [input] = useState(() => {
    const query = new URLSearchParams(window.location.search)
    return { state: query.get('state'), code: query.get('code'), error: query.get('error') }
  })
  const [error, setError] = useState('')
  const started = useRef(false)
  const authenticated = useAuthStore(state => state.isAuthenticated)
  const loading = useAuthStore(state => state.isLoading || !state.isMultiUserModeChecked)
  useEffect(() => { window.history.replaceState(null, '', '/agentbridge/servers/callback') }, [])
  useEffect(() => {
    if (loading || started.current) return
    started.current = true
    if (!authenticated || !input.state || !input.code || input.error) {
      setError('Sign in to the home account that started this connection, then link the server again from AgentBridge.')
      return
    }
    void api.post('/api/agentbridge/servers/callback', input).then(() => window.location.replace('/agentbridge')).catch(() => {
      setError('The connection could not be completed for this home account. Return to AgentBridge and start your own connection.')
    })
  }, [authenticated, loading, input])
  return <main className="flex min-h-screen items-center justify-center bg-background p-4 text-foreground"><div className="max-w-md space-y-4 rounded-xl border border-border p-6">
    <h1 className="text-xl font-semibold">Link AgentWorks servers</h1>
    {error ? <><p role="alert">{error}</p><a href="/agentbridge" className="text-primary underline">Return to AgentBridge</a></> : <p role="status">Completing your messaging connection…</p>}
  </div></main>
}
