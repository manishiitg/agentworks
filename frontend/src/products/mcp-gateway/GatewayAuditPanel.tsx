import { useState } from 'react'
import { ScrollText } from 'lucide-react'
import { SettingsCard, SettingsCount } from '../../components/ui/SettingsCard'
import { listAudit } from './gatewayAdminApi'
import { ConsoleEmpty, ConsoleError, ConsoleLoading } from './gatewayConsoleShared'
import { codeClass, tableClass, tdClass, thClass, useAttempt, useGatewayLoader } from './gatewayConsoleUtils'

const selectClass =
  'h-8 rounded-md border border-input bg-background px-2 text-xs shadow-sm focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring'

function outcomeDot(outcome: string): string {
  if (outcome === 'ok') return 'bg-green-500'
  if (outcome === 'denied') return 'bg-amber-500'
  return 'bg-red-500'
}

function formatTime(iso: string): string {
  const d = new Date(iso)
  return Number.isNaN(d.getTime()) ? iso : d.toLocaleString()
}

export function GatewayAuditPanel({ base }: { base: string }) {
  const [attempt, bump] = useAttempt()
  const [limit, setLimit] = useState(100)
  const { data, loading, error } = useGatewayLoader(async () => listAudit(base, limit), attempt)

  if (loading) return <ConsoleLoading label="Loading audit events…" />
  if (error || !data) return <ConsoleError message={error ?? 'Failed to load.'} onRetry={bump} />

  const events = [...data.events].reverse()

  return (
    <div className="space-y-4" data-testid="gateway-audit">
      <SettingsCard
        icon={<ScrollText className="h-4 w-4 text-primary" />}
        title="Audit log"
        count={<SettingsCount>{`${events.length} events`}</SettingsCount>}
        description="Every tool call through the gateway: who asked, what was decided, and how it ended. Newest first."
        actions={
          <select
            aria-label="Event limit"
            className={selectClass}
            value={limit}
            onChange={(e) => {
              setLimit(Number(e.target.value))
              bump()
            }}
          >
            {[50, 100, 500].map((n) => (
              <option key={n} value={n}>
                Last {n}
              </option>
            ))}
          </select>
        }
      >
        {events.length === 0 ? (
          <ConsoleEmpty>No tool calls yet. Calls appear here as clients use the gateway.</ConsoleEmpty>
        ) : (
          <table className={tableClass}>
            <thead>
              <tr>
                <th className={thClass}>Time</th>
                <th className={thClass}>User</th>
                <th className={thClass}>Tool</th>
                <th className={thClass}>Decision</th>
                <th className={thClass}>Outcome</th>
                <th className={thClass}>Duration</th>
              </tr>
            </thead>
            <tbody>
              {events.map((e) => (
                <tr key={e.ID}>
                  <td className={`${tdClass} whitespace-nowrap`}>{formatTime(e.Timestamp)}</td>
                  <td className={tdClass}>
                    <span className={codeClass}>{e.UserID || '—'}</span>
                  </td>
                  <td className={tdClass}>
                    <span className={codeClass}>{e.PublicName}</span>
                  </td>
                  <td className={tdClass}>{e.Decision}</td>
                  <td className={tdClass}>
                    <span className="inline-flex items-center gap-1.5">
                      <span className={`h-2 w-2 rounded-full ${outcomeDot(e.Outcome)}`} aria-hidden />
                      {e.Outcome}
                    </span>
                    {e.ErrorText && (
                      <span className="block max-w-60 truncate text-muted-foreground" title={e.ErrorText}>
                        {e.ErrorText}
                      </span>
                    )}
                  </td>
                  <td className={tdClass}>{e.DurationMs} ms</td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </SettingsCard>
    </div>
  )
}
