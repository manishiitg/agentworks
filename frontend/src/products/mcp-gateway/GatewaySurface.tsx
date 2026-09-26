import { useState } from 'react'
import { KeyRound, ScrollText, Server, UserRound, UsersRound } from 'lucide-react'
import { gatewayBaseUrl } from '../productSurfaceConfig'
import { ProductSurfaceSwitcher } from '../../components/ProductSurfaceSwitcher'
import { GatewayMark } from './GatewayMark'
import { listUsers } from './gatewayAdminApi'
import { GatewayServersPanel } from './GatewayServersPanel'
import { GatewayGrantsPanel } from './GatewayGrantsPanel'
import { GatewayGroupsPanel } from './GatewayGroupsPanel'
import { GatewayUsersPanel } from './GatewayUsersPanel'
import { GatewayAuditPanel } from './GatewayAuditPanel'
import { ConsoleError, ConsoleLoading } from './gatewayConsoleShared'
import { useAttempt, useGatewayLoader } from './gatewayConsoleUtils'

const SECTIONS = [
  { id: 'servers', label: 'Servers', icon: Server },
  { id: 'grants', label: 'Tools & Grants', icon: KeyRound },
  { id: 'groups', label: 'Groups', icon: UsersRound },
  { id: 'users', label: 'Users', icon: UserRound },
  { id: 'audit', label: 'Audit', icon: ScrollText },
] as const

type SectionId = (typeof SECTIONS)[number]['id']

/**
 * Embedded MCP Gateway console. The React UI talks to the gateway admin API
 * directly; locally the gateway trusts loopback callers as admin, so there
 * is no token prompt. Entry is hidden unless a gateway URL is configured;
 * the null branch only fires for a persisted surface after the URL was
 * removed.
 */
export function GatewaySurface() {
  const base = gatewayBaseUrl()
  const [section, setSection] = useState<SectionId>('servers')
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

  return (
    <div className="flex h-full flex-col bg-background" data-testid="gateway-surface">
      <header className="flex flex-wrap items-center gap-3 border-b border-border px-5 py-2.5">
        <ProductSurfaceSwitcher />
        <span className="flex items-center gap-2 border-l border-border pl-3">
          <GatewayMark className="h-6 w-6" />
          <span>
            <h1 className="text-sm font-semibold leading-tight text-foreground">MCP Gateway</h1>
            <p className="truncate text-xs leading-tight text-muted-foreground">
              Clients connect at <code className="rounded bg-muted px-1 font-mono text-[11px]">{`${base}/mcp`}</code>
            </p>
          </span>
        </span>
        <div className="ml-auto flex items-center">
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
        <div className="flex min-h-0 flex-1">
          <nav className="w-52 shrink-0 space-y-1 overflow-y-auto border-r border-border p-3" aria-label="Gateway sections">
            {SECTIONS.map(({ id, label, icon: Icon }) => (
              <button
                key={id}
                onClick={() => setSection(id)}
                aria-current={section === id ? 'page' : undefined}
                className={`flex w-full items-center gap-2.5 rounded-md px-3 py-2 text-sm font-medium ${
                  section === id
                    ? 'bg-muted text-foreground'
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
            {section === 'grants' && <GatewayGrantsPanel base={base} />}
            {section === 'groups' && <GatewayGroupsPanel base={base} />}
            {section === 'users' && <GatewayUsersPanel base={base} />}
            {section === 'audit' && <GatewayAuditPanel base={base} />}
          </div>
        </div>
      )}
    </div>
  )
}
