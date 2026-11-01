import { useEffect, useState } from 'react'
import api from '../services/api'
import { Button } from '../components/ui/Button'

type Consent = { client_name: string; redirect_uri: string; scopes: string[]; project?: { id: string; title: string; kind: string; access: 'view' | 'use' } | null }

const scopeDescriptions: Record<string, string> = {
  'vault:manage': 'Manage Vault MCP connections, groups, tool and regex permissions, and secret access as an administrator. Secret values are not returned',
  'vault:read': 'See Vault connections, tools, groups, who can reach what, the audit log, and run SQL that changes nothing',
  'vault:mcp': 'Use MCP tools allowed by your current Vault groups',
  'workflows:read': 'See workflows you can access and their setup',
  'files:read': 'Read workflow files, including test code',
  'relays:write': 'Create, edit files and dashboards, test and publish Flows within your current role. Builder chat uses your saved model and usage budget',
  'dashboards:read': 'Discover and view dashboards within your project access; private Code dashboards stay yours',
  'builder:chat': 'Edit workflow plans directly, without a model turn, using native validation, revisions and the plan changelog. Edit code, files and dashboards within your current role and connection limits. Builder chat also uses your saved model in your existing chat',
  'runs:execute': 'Start, watch, and cancel workflow runs',
  'crews:read': 'See Crews you can use, their functions, and project files (never their private chats)',
  'crews:run': 'Ask Crews questions and call their functions; the work runs in each Crew\'s own chat',
  'crews:write': 'Create Crews and edit the Crews you own (identity, skills, MCP connections, references, functions, schedules, files, dashboards). Run sandboxed shell commands, control the Crew browser, mutate its database and use its connected tools; execution is recorded in your owner-only audit history. With access to all your Crews, manage approved account-wide folder attachments',
  'knowledgebase:read': 'Read shared Brain folders your identity can access',
  'knowledgebase:write': 'Save shared knowledge within your folder grants',
  'code:run': 'Send messages into your own Code projects and read the replies, so the agent runs commands in your project\'s sandbox. Includes editing dashboards in your own projects within your current role',
  'crews:review': 'Review every Code workspace: cost, chats and files. Every view is recorded in the audit log',
  'users:manage': 'See everyone\'s token use on the shared accounts and set their daily and weekly limits, as an administrator. Every change is recorded in the audit log',
}

// The page leads with a few plain lines, one per kind of access; the exact permissions sit behind "Show details".
const scopeGroups: { summary: string; scopes: string[] }[] = [
  { summary: 'Manage Vault connections, groups and permissions (administrator)', scopes: ['vault:manage'] },
  { summary: 'See Vault setup, access and the audit log', scopes: ['vault:read'] },
  { summary: 'Use Vault MCP tools you are allowed to use', scopes: ['vault:mcp'] },
  { summary: 'See and run your workflows', scopes: ['workflows:read', 'files:read', 'runs:execute'] },
  { summary: 'Discover and view your accessible dashboards', scopes: ['dashboards:read'] },
  { summary: 'Use your Crews', scopes: ['crews:read', 'crews:run'] },
  { summary: 'Edit your own Crews and run their sandboxed commands, browser, database and connected tools (logged)', scopes: ['crews:write'] },
  { summary: 'Edit Flows and workflows, as far as your role allows', scopes: ['builder:chat', 'relays:write'] },
  { summary: 'Use shared Brain within your folder grants', scopes: ['knowledgebase:read', 'knowledgebase:write'] },
  { summary: 'Run your own Code: send it messages and read the replies (it runs commands in your project)', scopes: ['code:run'] },
  { summary: 'Review Code workspaces (logged)', scopes: ['crews:review'] },
  { summary: 'Set people\'s shared-account token limits (administrator, logged)', scopes: ['users:manage'] },
]

export function MCPOAuthConsent() {
  const vault = window.location.pathname === '/oauth/vault'
  const consentAPI = vault ? '/api/oauth/vault/consent' : '/api/oauth/mcp/consent'
  const request = new URLSearchParams(window.location.search).get('request')
  const [consent, setConsent] = useState<Consent | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)

  useEffect(() => {
    if (!request || !/^mcp_req_[a-f0-9]{64}$/.test(request)) { setError('This connection request is invalid.'); return }
    api.get<Consent>(consentAPI, { params: { request } })
      .then(({ data }) => setConsent(data))
      .catch(() => setError('This connection request has expired. Start again in your AI app.'))
  }, [request, consentAPI])


  const decide = async (decision: 'approve' | 'deny') => {
    if (!consent || !request) return
    setBusy(true)
    setError(null)
    try {
      const { data } = await api.post<{ redirect_url: string }>(`${consentAPI}?request=${encodeURIComponent(request)}`, { decision, workflow_ids: [] })
      const destination = new URL(data.redirect_url)
      if (destination.origin !== new URL(consent.redirect_uri).origin || destination.pathname !== new URL(consent.redirect_uri).pathname) {
        throw new Error('The connection response had an unexpected destination.')
      }
      window.location.assign(data.redirect_url)
    } catch (err) {
      const reason = (err as { response?: { data?: { error_description?: string } } })?.response?.data?.error_description
      setError(reason || (err instanceof Error ? err.message : 'Could not complete the connection.'))
      setBusy(false)
    }
  }

  return <main className="min-h-screen bg-background flex items-center justify-center p-4">
    <div className="w-full max-w-md rounded-xl border border-border bg-card p-6 shadow-sm space-y-5">
      <div>
        <h1 className="text-xl font-semibold text-foreground">Connect to {vault ? 'Vault' : 'AgentWorks'}</h1>
        <p className="mt-1 text-sm text-muted-foreground">{consent ? (consent.project ? `${consent.client_name} wants access to ${consent.project.title} only.` : vault ? `${consent.client_name} wants access to your permitted MCP tools.` : consent.scopes.includes('vault:manage') ? `${consent.client_name} wants platform access, including Vault administration.` : consent.scopes.some(scope => scope.startsWith('crews:')) ? `${consent.client_name} wants access to your workflows and Crews.` : `${consent.client_name} wants access to your workflows.`) : 'Loading connection request…'}</p>
      </div>
      {consent && <>
        {vault
          ? <ul className="space-y-2 text-sm text-foreground">{scopeGroups.filter(group => group.scopes.some(scope => consent.scopes.includes(scope))).map(group => <li key={group.summary} className="rounded-md bg-muted p-3">{group.summary}</li>)}</ul>
          : consent.project ? <div className="rounded-md bg-muted p-3 text-sm text-foreground"><p className="font-medium">{consent.project.title} only</p><p className="mt-1">{consent.project.access === 'use' ? 'View and use this project within your current permissions.' : 'View this project within your current permissions.'} Other projects and project settings are excluded. Removing your project access ends this connection’s access.</p></div>
          : <p className="rounded-md bg-muted p-3 text-sm text-foreground">This app can do what you can do on the website, and nothing more. Your role on each Crew, workflow or project still decides what it may change.</p>}
        <details className="text-xs text-muted-foreground">
          <summary className="cursor-pointer">Show details</summary>
          <ul className="mt-2 space-y-1">{consent.scopes.map(scope => <li key={scope}>{scopeDescriptions[scope] || scope}</li>)}</ul>
        </details>
        <p className="text-xs text-muted-foreground break-all">You will return to {new URL(consent.redirect_uri).origin}. You can revoke this connection later.</p>
        <div className="flex gap-2 justify-end">
          <Button variant="outline" disabled={busy} onClick={() => void decide('deny')}>Deny</Button>
          <Button disabled={busy} onClick={() => void decide('approve')}>{busy ? 'Connecting…' : 'Allow access'}</Button>
        </div>
      </>}
      {error && <p role="alert" className="text-sm text-destructive">{error}</p>}
    </div>
  </main>
}
