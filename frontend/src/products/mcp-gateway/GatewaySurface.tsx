import { useEffect, useState, type FormEvent } from 'react'
import { PlugZap, ScrollText, Server, ShieldCheck, UserRound, UsersRound } from 'lucide-react'
import { gatewayBaseUrl } from '../productSurfaceConfig'
import { ProductSurfaceSwitcher } from '../../components/ProductSurfaceSwitcher'
import { GatewayServersPanel } from './GatewayServersPanel'
import { GatewayGroupsPanel } from './GatewayGroupsPanel'
import { GatewayUsersPanel } from './GatewayUsersPanel'
import { GatewayAuditPanel } from './GatewayAuditPanel'
import { GatewayConnectPanel } from './GatewayConnectPanel'
import { GatewayPIIPanel } from './GatewayPIIPanel'
import { GATEWAY_AUTH_REQUIRED_EVENT, listUsers, setGatewayAdminToken } from './gatewayAdminApi'

const SECTIONS = [
  { id: 'servers', label: 'MCP Gateway', icon: Server },
  { id: 'groups', label: 'Groups', icon: UsersRound },
  { id: 'users', label: 'Users', icon: UserRound },
  { id: 'audit', label: 'Audit', icon: ScrollText },
  { id: 'pii', label: 'PII policy', icon: ShieldCheck },
  { id: 'connect', label: 'Connect', icon: PlugZap },
] as const

type SectionId = (typeof SECTIONS)[number]['id']

/**
 * Embedded CapLayer console. Its MCP Gateway UI talks to the gateway admin API
 * directly. Entry is hidden unless a gateway URL is configured;
 * the null branch only fires for a persisted surface after the URL was
 * removed.
 */
export function GatewaySurface() {
  const base = gatewayBaseUrl()
  const [section, setSection] = useState<SectionId>('servers')
  const [authRequired, setAuthRequired] = useState(false)
  const [token, setToken] = useState('')
  const [authError, setAuthError] = useState('')
  const [authBusy, setAuthBusy] = useState(false)
  const [revision, setRevision] = useState(0)

  useEffect(() => {
    const onAuthRequired = (event: Event) => {
      if ((event as CustomEvent<string>).detail === base) setAuthRequired(true)
    }
    window.addEventListener(GATEWAY_AUTH_REQUIRED_EVENT, onAuthRequired)
    return () => window.removeEventListener(GATEWAY_AUTH_REQUIRED_EVENT, onAuthRequired)
  }, [base])

  async function submitToken(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    if (!base || !token.trim()) return
    setAuthBusy(true)
    setAuthError('')
    setGatewayAdminToken(base, token.trim())
    try {
      await listUsers(base)
      setToken('')
      setAuthRequired(false)
      setRevision(value => value + 1)
    } catch (error) {
      setAuthError(error instanceof Error ? error.message : 'Could not sign in')
    } finally {
      setAuthBusy(false)
    }
  }

  if (!base) {
    return (
      <div className="flex h-full items-center justify-center p-8 text-center text-sm text-slate-500">
        CapLayer needs an MCP Gateway endpoint for this deployment.
      </div>
    )
  }

  return (
    <div className="flex h-full flex-col bg-muted" data-testid="gateway-surface">
      <header className="flex flex-wrap items-center gap-3 border-b border-border px-5 py-2.5">
        <ProductSurfaceSwitcher />
      </header>

      {authRequired ? (
        <main className="grid min-h-0 flex-1 place-items-center p-6">
          <form onSubmit={event => void submitToken(event)} className="w-full max-w-sm space-y-3 rounded-md border border-border bg-card p-6" data-testid="gateway-admin-login">
            <h2 className="text-lg font-semibold text-foreground">Sign in to CapLayer</h2>
            <p className="text-sm text-muted-foreground">Enter the admin token from the file path printed by the gateway launcher.</p>
            <input aria-label="Admin token" type="password" autoComplete="off" value={token} onChange={event => setToken(event.target.value)} className="w-full rounded-md border border-input bg-background px-3 py-2 text-sm" />
            {authError && <p role="alert" className="text-sm text-destructive">{authError}</p>}
            <button type="submit" disabled={authBusy || !token.trim()} className="rounded-md bg-primary px-4 py-2 text-sm text-primary-foreground disabled:opacity-50">{authBusy ? 'Signing in…' : 'Sign in'}</button>
          </form>
        </main>
      ) : <div key={revision} className="flex min-h-0 flex-1">
        <nav className="w-52 shrink-0 space-y-1 overflow-y-auto border-r border-border p-3" aria-label="CapLayer sections">
          {SECTIONS.map(({ id, label, icon: Icon }) => (
            <button
              key={id}
              onClick={() => setSection(id)}
              aria-current={section === id ? 'page' : undefined}
              className={`flex w-full items-center gap-2.5 rounded-md px-3 py-2 text-sm font-medium ${
                section === id
                  ? 'bg-card text-foreground'
                  : 'text-muted-foreground hover:bg-muted/60 hover:text-foreground'
              }`}
            >
              <Icon className="h-4 w-4" aria-hidden />
              {label}
            </button>
          ))}
        </nav>
        <div className="min-h-0 min-w-0 flex-1 overflow-y-auto p-4">
          {section === 'servers' && <GatewayServersPanel base={base} />}
          {section === 'groups' && <GatewayGroupsPanel base={base} />}
          {section === 'users' && <GatewayUsersPanel base={base} />}
          {section === 'audit' && <GatewayAuditPanel base={base} />}
          {section === 'pii' && <GatewayPIIPanel base={base} />}
          {section === 'connect' && <GatewayConnectPanel base={base} />}
        </div>
      </div>}
    </div>
  )
}
