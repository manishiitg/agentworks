import { useState } from 'react'
import { ExternalLink, KeyRound, PlugZap, ScrollText, UserRound, UsersRound } from 'lucide-react'
import { gatewayAdminUrl, gatewayBaseUrl } from '../productSurfaceConfig'
import { GatewayMark } from './GatewayMark'
import { listUsers } from './gatewayAdminApi'
import { GatewayConnectionsPanel } from './GatewayConnectionsPanel'
import { GatewayGrantsPanel } from './GatewayGrantsPanel'
import { GatewayGroupsPanel } from './GatewayGroupsPanel'
import { GatewayUsersPanel } from './GatewayUsersPanel'
import { GatewayAuditPanel } from './GatewayAuditPanel'
import { ConsoleError, ConsoleLoading } from './gatewayConsoleShared'
import { useAttempt, useGatewayLoader } from './gatewayConsoleUtils'

const TABS = [
  { id: 'connections', label: 'Connections', icon: PlugZap },
  { id: 'grants', label: 'Tools & Grants', icon: KeyRound },
  { id: 'groups', label: 'Groups', icon: UsersRound },
  { id: 'users', label: 'Users', icon: UserRound },
  { id: 'audit', label: 'Audit', icon: ScrollText },
] as const

type TabId = (typeof TABS)[number]['id']

/**
 * Embedded MCP Gateway console. The React UI talks to the gateway admin API
 * directly; locally the gateway trusts loopback callers as admin, so there
 * is no token prompt. Entry is hidden unless a gateway URL is configured;
 * the null branch only fires for a persisted surface after the URL was
 * removed.
 */
export function GatewaySurface() {
  const base = gatewayBaseUrl()
  const [tab, setTab] = useState<TabId>('connections')
  const [attempt, bump] = useAttempt()
  const ping = useGatewayLoader(async () => {
    if (!base) throw new Error('No MCP Gateway is configured for this deployment.')
    await listUsers(base)
  }, attempt)

  if (!base) {
    return (
      <div className="flex h-full items-center justify-center p-8 text-center text-sm text-slate-500">
        No MCP Gateway is configured for this deployment.
      </div>
    )
  }

  const adminUrl = gatewayAdminUrl()

  return (
    <div className="flex h-full flex-col bg-background" data-testid="gateway-surface">
      <header className="flex flex-wrap items-center gap-3 border-b border-border px-5 py-3">
        <GatewayMark className="h-8 w-8" />
        <div className="min-w-0">
          <h1 className="text-base font-semibold text-foreground">MCP Gateway</h1>
          <p className="truncate text-xs text-muted-foreground">
            Clients connect at <code className="rounded bg-muted px-1 py-0.5 font-mono text-[11px]">{`${base}/mcp`}</code>
          </p>
        </div>
        <div className="ml-auto flex items-center gap-3">
          {ping.loading ? (
            <span className="inline-flex items-center gap-1.5 text-xs text-muted-foreground">
              <span className="h-2 w-2 animate-pulse rounded-full bg-gray-400" aria-hidden />
              Connecting…
            </span>
          ) : ping.error ? (
            <span className="inline-flex items-center gap-1.5 text-xs text-muted-foreground">
              <span className="h-2 w-2 rounded-full bg-red-500" aria-hidden />
              Unreachable
            </span>
          ) : (
            <span className="inline-flex items-center gap-1.5 text-xs text-muted-foreground">
              <span className="h-2 w-2 rounded-full bg-green-500" aria-hidden />
              Connected
            </span>
          )}
          {adminUrl && (
            <a
              href={adminUrl}
              target="_blank"
              rel="noreferrer"
              className="inline-flex items-center gap-1 text-xs text-muted-foreground hover:text-foreground"
            >
              Classic admin
              <ExternalLink className="h-3 w-3" aria-hidden />
            </a>
          )}
        </div>
      </header>

      {ping.loading ? (
        <div className="p-5">
          <ConsoleLoading label="Connecting to the gateway…" />
        </div>
      ) : ping.error ? (
        <div className="p-5">
          <ConsoleError message={ping.error} onRetry={bump} />
        </div>
      ) : (
        <>
          <nav className="flex gap-1 overflow-x-auto border-b border-border px-5" role="tablist" aria-label="Gateway sections">
            {TABS.map(({ id, label, icon: Icon }) => (
              <button
                key={id}
                role="tab"
                aria-selected={tab === id}
                onClick={() => setTab(id)}
                className={`inline-flex items-center gap-1.5 whitespace-nowrap border-b-2 px-3 py-2.5 text-sm font-medium ${
                  tab === id
                    ? 'border-primary text-foreground'
                    : 'border-transparent text-muted-foreground hover:border-border hover:text-foreground'
                }`}
              >
                <Icon className="h-4 w-4" aria-hidden />
                {label}
              </button>
            ))}
          </nav>
          <div className="min-h-0 flex-1 overflow-y-auto p-5" role="tabpanel">
            <div className="mx-auto w-full max-w-5xl">
              {tab === 'connections' && <GatewayConnectionsPanel base={base} />}
              {tab === 'grants' && <GatewayGrantsPanel base={base} />}
              {tab === 'groups' && <GatewayGroupsPanel base={base} />}
              {tab === 'users' && <GatewayUsersPanel base={base} />}
              {tab === 'audit' && <GatewayAuditPanel base={base} />}
            </div>
          </div>
        </>
      )}
    </div>
  )
}
