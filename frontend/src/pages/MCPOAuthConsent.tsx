import { useEffect, useState } from 'react'
import api from '../services/api'
import { Button } from '../components/ui/Button'

type Consent = { client_name: string; redirect_uri: string; scopes: string[]; editable_workflows?: { id: string; label: string }[] }

const scopeDescriptions: Record<string, string> = {
  'workflows:read': 'See workflows you can access and their setup',
  'files:read': 'Read workflow files, including test code',
  'builder:chat': 'Edit plans and code in selected workflows using their Builder model in your existing chat',
  'runs:execute': 'Start, watch, and cancel workflow runs',
  'crews:read': 'See Crews you can use, their functions, and project files (never their private chats)',
  'crews:run': 'Ask Crews questions and call their functions; the work runs in each Crew\'s own chat',
  'crews:write': 'Create Crews and edit the Crews you own (identity, skills, functions, schedules, files)',
  'code:review': 'Review every Code workspace: cost, chats and files, read-only. Every view is recorded in the audit log',
}

export function MCPOAuthConsent() {
  const request = new URLSearchParams(window.location.search).get('request')
  const [consent, setConsent] = useState<Consent | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)
  const [workflowIDs, setWorkflowIDs] = useState<string[]>([])
  const builder = consent?.scopes.includes('builder:chat') ?? false

  useEffect(() => {
    if (!request || !/^mcp_req_[a-f0-9]{64}$/.test(request)) { setError('This connection request is invalid.'); return }
    api.get<Consent>('/api/oauth/mcp/consent', { params: { request } })
      .then(({ data }) => setConsent(data))
      .catch(() => setError('This connection request has expired. Start again in your AI app.'))
  }, [request])

  const decide = async (decision: 'approve' | 'deny') => {
    if (!consent || !request) return
    setBusy(true)
    setError(null)
    try {
      const { data } = await api.post<{ redirect_url: string }>(`/api/oauth/mcp/consent?request=${encodeURIComponent(request)}`, { decision, workflow_ids: builder ? workflowIDs : [] })
      const destination = new URL(data.redirect_url)
      if (destination.origin !== new URL(consent.redirect_uri).origin || destination.pathname !== new URL(consent.redirect_uri).pathname) {
        throw new Error('The connection response had an unexpected destination.')
      }
      window.location.assign(data.redirect_url)
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Could not complete the connection.')
      setBusy(false)
    }
  }

  return <main className="min-h-screen bg-background flex items-center justify-center p-4">
    <div className="w-full max-w-md rounded-xl border border-border bg-card p-6 shadow-sm space-y-5">
      <div>
        <h1 className="text-xl font-semibold text-foreground">Connect to AgentWorks</h1>
        <p className="mt-1 text-sm text-muted-foreground">{consent ? (consent.scopes.some(scope => scope.startsWith('crews:')) ? `${consent.client_name} wants access to your workflows and Crews.` : `${consent.client_name} wants access to your workflows.`) : 'Loading connection request…'}</p>
      </div>
      {consent && <>
        <ul className="space-y-2 text-sm text-foreground">{consent.scopes.map(scope => <li key={scope} className="rounded-md bg-muted p-3">{scopeDescriptions[scope] || scope}</li>)}</ul>
        {builder && <fieldset className="space-y-2">
          <legend className="text-sm font-medium">Workflows this connection may build</legend>
          <p className="text-xs text-muted-foreground">Reading and running are also limited to this selection. Builder uses your saved model and normal usage budget.</p>
          {(consent.editable_workflows ?? []).map(workflow => <label key={workflow.id} className="flex items-center gap-2 text-sm">
            <input type="checkbox" checked={workflowIDs.includes(workflow.id)} disabled={busy} onChange={event => setWorkflowIDs(current => event.target.checked ? [...current, workflow.id] : current.filter(id => id !== workflow.id))} />
            {workflow.label || workflow.id}
          </label>)}
          {!consent.editable_workflows?.length && <p className="text-sm text-muted-foreground">You have no editable workflows. Ask a workflow owner for write access before connecting Builder.</p>}
        </fieldset>}
        <p className="text-xs text-muted-foreground break-all">You will return to {new URL(consent.redirect_uri).origin}. You can revoke this connection later.</p>
        <div className="flex gap-2 justify-end">
          <Button variant="outline" disabled={busy} onClick={() => void decide('deny')}>Deny</Button>
          <Button disabled={busy || (builder && workflowIDs.length === 0)} onClick={() => void decide('approve')}>{busy ? 'Connecting…' : 'Allow access'}</Button>
        </div>
      </>}
      {error && <p role="alert" className="text-sm text-destructive">{error}</p>}
    </div>
  </main>
}
