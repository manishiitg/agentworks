import { useEffect, useState } from 'react'
import { CheckCircle2, FolderLock, Laptop, Loader2, MessageCircle, ShieldCheck, XCircle } from 'lucide-react'
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

  const messagingOnly = consent?.scopes.every(scope => scope.startsWith('messages:'))
  const connectsComputer = consent?.scopes.includes('devices:connect')
  const additionalScopes = consent?.scopes.filter(scope => scope !== 'devices:connect' && !scope.startsWith('messages:')) ?? []
  const StatusIcon = decision === 'approve' ? CheckCircle2 : decision === 'deny' ? XCircle : Laptop

  return <main className="flex min-h-dvh items-center justify-center bg-background p-4 text-foreground sm:p-6">
    <section aria-labelledby="cli-consent-title" className="w-full max-w-md rounded-2xl border border-border bg-card p-5 shadow-sm sm:p-7">
      <div className="mb-5 flex items-center gap-2.5 text-sm font-medium">
        <span className="flex h-9 w-9 items-center justify-center rounded-xl bg-primary/10 text-primary"><StatusIcon className="h-5 w-5" aria-hidden="true" /></span>
        AgentWorks
      </div>
      <h1 id="cli-consent-title" className="text-2xl font-semibold tracking-tight">{decision === 'approve' ? 'You’re connected' : decision === 'deny' ? 'Connection declined' : connectsComputer ? 'Connect this computer' : 'Connect your AI agent'}</h1>
      <p className="mt-2 text-sm leading-6 text-muted-foreground">{decision ? 'You can close this page and return to your app.' : 'Match this code with the one shown on your computer.'}</p>

      {consent && !decision && <>
        <div className="my-5 rounded-xl border border-border bg-muted/40 px-4 py-4 text-center">
          <p className="text-xs font-medium text-muted-foreground">Verification code</p>
          <p aria-label="Verification code" className="mt-2 select-all font-mono text-3xl font-semibold tracking-[0.16em]">{consent.user_code}</p>
        </div>
        <ul aria-label="Connection permissions" className="space-y-3 text-sm">
          {messagingOnly && <>
            <li className="flex items-start gap-2.5"><MessageCircle className="mt-0.5 h-4 w-4 shrink-0 text-muted-foreground" aria-hidden="true" /><span>Find agents and exchange messages.</span></li>
            <li className="flex items-start gap-2.5"><ShieldCheck className="mt-0.5 h-4 w-4 shrink-0 text-muted-foreground" aria-hidden="true" /><span>Files and AI accounts stay private.</span></li>
          </>}
          {connectsComputer && <>
            <li className="flex items-start gap-2.5"><MessageCircle className="mt-0.5 h-4 w-4 shrink-0 text-muted-foreground" aria-hidden="true" /><span>Run commands you allow.</span></li>
            <li className="flex items-start gap-2.5"><FolderLock className="mt-0.5 h-4 w-4 shrink-0 text-muted-foreground" aria-hidden="true" /><span>Read or edit folders you share.</span></li>
          </>}
        </ul>
        {connectsComputer && <p className="mt-2 text-xs text-muted-foreground">No folder is shared automatically.</p>}
        {additionalScopes.length > 0 && <section aria-label="Additional permissions" className="mt-4 text-sm">
          <h2 className="font-medium">Also requested</h2>
          <ul className="mt-1 space-y-1 break-words text-xs text-muted-foreground">{consent.scopes.filter(scope => scope !== 'devices:connect').map(scope => <li key={scope}>{scope}</li>)}</ul>
          <p className="mt-2 text-xs text-muted-foreground">Your current account access applies to every request.</p>
        </section>}
        <details className="mt-4 text-xs text-muted-foreground">
          <summary className="cursor-pointer rounded py-1 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring">Connection details</summary>
          <div className="mt-2 space-y-2 leading-5">
            <p>Requested by {consent.client_name}.</p>
            <p>Approve only if you started <code>agentworks start</code> or <code>agentworks login</code>.</p>
            {connectsComputer && <p>Choose folders and Read or Read &amp; write in Crew Settings → Laptop access. Stopping the CLI disconnects laptop access and pauses messaging.</p>}
            {messagingOnly && <p>Your chats stay on your own computer. This network does not get access to your files or AI accounts.</p>}
          </div>
        </details>
        <p className="mb-3 mt-5 text-xs text-muted-foreground">Approve only if you started this connection.</p>
        <div className="flex flex-col gap-2">
          <Button className="h-11 w-full" disabled={busy} onClick={() => void decide('approve')}>{busy ? 'Please wait…' : 'Approve connection'}</Button>
          <Button variant="ghost" className="h-10 w-full" disabled={busy} onClick={() => void decide('deny')}>Deny</Button>
        </div>
      </>}
      {!consent && !error && <p role="status" className="mt-6 flex items-center gap-2 text-sm text-muted-foreground"><Loader2 className="h-4 w-4 animate-spin" aria-hidden="true" />Loading connection…</p>}
      {error && <p role="alert" className="mt-4 text-sm text-destructive">{error}</p>}
    </section>
  </main>
}
