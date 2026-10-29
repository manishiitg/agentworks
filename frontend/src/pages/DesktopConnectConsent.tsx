import { useState } from 'react'
import { authApi } from '../services/api'
import { useAuthStore } from '../stores/useAuthStore'
import { desktopConsentReturnPath } from '../utils/mcpOAuthReturn'

export function DesktopConnectConsent() {
  const user = useAuthStore(s => s.user)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const valid = desktopConsentReturnPath(window.location.pathname + window.location.search)
  const connect = async () => {
    if (!valid) return
    setBusy(true); setError('')
    try {
      const params = new URL(valid, window.location.origin).searchParams
      const response = await authApi.createDesktopConnect(params.get('challenge')!)
      const code = new URL(response.connect_url).searchParams.get('code')
      if (!code) throw new Error('No sign-in code returned')
      const callback = new URL(params.get('callback')!)
      callback.searchParams.set('state', params.get('state')!)
      callback.searchParams.set('code', code)
      window.location.replace(callback.href)
    } catch { setError('Could not connect. Return to the app and try again.'); setBusy(false) }
  }
  return <main className="grid min-h-screen place-items-center bg-background p-6 text-foreground">
    <section className="w-full max-w-md space-y-5 rounded-xl border border-border p-6">
      <h1 className="text-xl font-semibold">Connect your Mac app</h1>
      <p>Use {window.location.host} in AgentWorks as <strong>{user?.email || user?.username}</strong>.</p>
      <p className="text-sm text-muted-foreground">Your existing access applies. Chats, files and agents stay on this server. Connect only if you started this from your Mac app.</p>
      {!valid && <p role="alert">This link is invalid. Start again from Connect server in the app.</p>}
      {error && <p role="alert">{error}</p>}
      <button disabled={!valid || busy} onClick={() => void connect()} className="rounded-lg bg-primary px-4 py-2 text-primary-foreground disabled:opacity-50">{busy ? 'Connecting…' : 'Connect this account'}</button>
    </section>
  </main>
}
