import { useEffect, useState } from 'react'
import api from '../services/api'
import { Button } from '../components/ui/Button'

type Consent = { client_name: string; scopes: string[]; user_code: string }

export function CLIOAuthConsent() {
  const code = new URLSearchParams(window.location.search).get('code')
  const [consent, setConsent] = useState<Consent | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [decision, setDecision] = useState<'approve' | 'deny' | null>(null)
  const [busy, setBusy] = useState(false)

  useEffect(() => {
    if (!code || !/^cli_verify_[a-f0-9]{64}$/.test(code)) { setError('This CLI sign-in link is invalid.'); return }
    api.get<Consent>('/api/oauth/cli/consent', { params: { code } })
      .then(({ data }) => setConsent(data))
      .catch(() => setError('This CLI sign-in link has expired. Run agentworks login again.'))
  }, [code])

  const decide = async (value: 'approve' | 'deny') => {
    if (!code) return
    setBusy(true)
    setError(null)
    try {
      await api.post(`/api/oauth/cli/consent?code=${encodeURIComponent(code)}`, { decision: value })
      setDecision(value)
    } catch { setError('Could not complete CLI sign-in. Run agentworks login again.') }
    finally { setBusy(false) }
  }

  return <main className="min-h-screen bg-background flex items-center justify-center p-4">
    <div className="w-full max-w-md rounded-xl border border-border bg-card p-6 shadow-sm space-y-5">
      <div>
        <h1 className="text-xl font-semibold text-foreground">Sign in to AgentWorks CLI</h1>
        <p className="mt-1 text-sm text-muted-foreground">{decision ? 'You can return to your terminal.' : 'Approve the CLI running on your computer.'}</p>
      </div>
      {consent && !decision && <>
        <p className="text-sm text-foreground">Only allow this if you started <code>agentworks login</code> and your terminal shows this code: <strong className="font-mono">{consent.user_code}</strong></p>
        {consent.scopes.includes('devices:connect') && <p className="text-sm text-foreground">Connect a local file executor to your website account. Only folders you explicitly select in the executor command are shared. Read-only folders cannot be edited; plans and private files stay protected. Stopping the command disconnects it.</p>}
        {consent.scopes.includes('files:write') && <p className="text-sm text-foreground">Write workflow source and documentation with revision checks. Plans, configuration, databases and private files stay protected.</p>}
        {consent.scopes.some(scope => scope !== 'devices:connect') && <p className="text-sm text-foreground">Approved permissions: {consent.scopes.filter(scope => scope !== 'devices:connect').join(', ')}. Your current account access applies to every request.</p>}
        <div className="flex justify-end gap-2">
          <Button variant="outline" disabled={busy} onClick={() => void decide('deny')}>Deny</Button>
          <Button disabled={busy} onClick={() => void decide('approve')}>{busy ? 'Connecting…' : 'Allow access'}</Button>
        </div>
      </>}
      {decision === 'approve' && <p className="text-sm text-foreground">Your CLI is connected. This window can be closed.</p>}
      {decision === 'deny' && <p className="text-sm text-foreground">Connection denied. This window can be closed.</p>}
      {error && <p role="alert" className="text-sm text-destructive">{error}</p>}
    </div>
  </main>
}
