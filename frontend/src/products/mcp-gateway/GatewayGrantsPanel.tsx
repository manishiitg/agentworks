import { useEffect, useState } from 'react'
import { KeyRound, Loader2 } from 'lucide-react'
import { SettingsCard, SettingsCount } from '../../components/ui/SettingsCard'
import { Checkbox } from '../../components/ui/checkbox'
import { getGrants, listGroups, listTools, listUsers, setGrant } from './gatewayAdminApi'
import { ConsoleEmpty, ConsoleError, ConsoleLoading } from './gatewayConsoleShared'
import {
  codeClass,
  gatewayErrorMessage,
  tableClass,
  tdClass,
  thClass,
  useAttempt,
  useGatewayLoader,
} from './gatewayConsoleUtils'

const selectClass =
  'h-8 rounded-md border border-input bg-background px-2 text-xs shadow-sm focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring'

type Subject = { kind: 'user'; id: string } | { kind: 'group'; id: string }

export function GatewayGrantsPanel({ base }: { base: string }) {
  const [attempt, bump] = useAttempt()
  const { data, loading, error } = useGatewayLoader(async () => {
    const [tools, users, groups] = await Promise.all([listTools(base), listUsers(base), listGroups(base)])
    return { tools: tools.tools, users: users.users, groups: groups.groups }
  }, attempt)

  const [subject, setSubject] = useState<Subject | null>(null)
  const [grants, setGrants] = useState<Set<string> | null>(null)
  const [grantsError, setGrantsError] = useState<string | null>(null)
  const [toggling, setToggling] = useState<string | null>(null)

  useEffect(() => {
    if (!data || subject) return
    if (data.users.length > 0) setSubject({ kind: 'user', id: data.users[0].ID })
    else if (data.groups.length > 0) setSubject({ kind: 'group', id: data.groups[0].ID })
  }, [data, subject])

  useEffect(() => {
    if (!subject) return
    let cancelled = false
    setGrants(null)
    setGrantsError(null)
    void getGrants(base, subject.kind === 'user' ? { user: subject.id } : { group: subject.id }).then(
      (list) => {
        if (!cancelled) setGrants(new Set(list))
      },
      (err: unknown) => {
        if (!cancelled) setGrantsError(gatewayErrorMessage(err))
      },
    )
    return () => {
      cancelled = true
    }
  }, [base, subject, attempt])

  async function onToggle(tool: string, grant: boolean) {
    if (!subject) return
    setToggling(tool)
    try {
      await setGrant(base, subject.kind === 'user' ? { user: subject.id } : { group: subject.id }, tool, grant)
      setGrants((prev) => {
        const next = new Set(prev ?? [])
        if (grant) next.add(tool)
        else next.delete(tool)
        return next
      })
    } catch (err: unknown) {
      setGrantsError(gatewayErrorMessage(err))
    } finally {
      setToggling(null)
    }
  }

  if (loading) return <ConsoleLoading label="Loading tools…" />
  if (error || !data) return <ConsoleError message={error ?? 'Failed to load.'} onRetry={bump} />

  return (
    <div className="space-y-4" data-testid="gateway-grants">
      <SettingsCard
        icon={<KeyRound className="h-4 w-4 text-primary" />}
        title="Tool grants"
        count={<SettingsCount>{`${data.tools.length} tools`}</SettingsCount>}
        description="Deny-by-default: a tool is invisible to everyone until granted to a user or a group. Pick a subject, then tick the tools they may call."
      >
        <div className="flex flex-wrap items-center gap-2">
          <div className="flex gap-1 rounded-md bg-muted p-1 text-xs" role="tablist" aria-label="Subject kind">
            {(['user', 'group'] as const).map((kind) => (
              <button
                key={kind}
                role="tab"
                aria-selected={subject?.kind === kind}
                onClick={() => {
                  const list = kind === 'user' ? data.users : data.groups
                  setSubject(list.length > 0 ? { kind, id: list[0].ID } : null)
                }}
                className={`rounded px-3 py-1 font-medium capitalize ${
                  subject?.kind === kind ? 'bg-background text-foreground shadow-sm' : 'text-muted-foreground hover:text-foreground'
                }`}
              >
                {kind}
              </button>
            ))}
          </div>
          <select
            aria-label={subject?.kind === 'group' ? 'Group' : 'User'}
            className={selectClass}
            value={subject?.id ?? ''}
            onChange={(e) => {
              const kind = subject?.kind ?? 'user'
              setSubject(e.target.value ? { kind, id: e.target.value } : null)
            }}
            data-testid="gateway-grant-subject"
          >
            {(subject?.kind === 'group' ? data.groups : data.users).map((s) => (
              <option key={s.ID} value={s.ID}>
                {s.ID}
              </option>
            ))}
          </select>
        </div>

        {grantsError && <ConsoleError message={grantsError} onRetry={bump} />}
        {!subject ? (
          <ConsoleEmpty>Create a user or a group first, then grant tools to them.</ConsoleEmpty>
        ) : grants === null ? (
          <ConsoleLoading label="Loading grants…" />
        ) : data.tools.length === 0 ? (
          <ConsoleEmpty>No tools discovered yet. Connect a server first.</ConsoleEmpty>
        ) : (
          <table className={tableClass}>
            <thead>
              <tr>
                <th className={thClass}>Tool</th>
                <th className={thClass}>Upstream</th>
                <th className={thClass}>Description</th>
                <th className={thClass}>Granted</th>
              </tr>
            </thead>
            <tbody>
              {data.tools.map((t) => {
                const granted = grants.has(t.PublicName)
                return (
                  <tr key={t.PublicName}>
                    <td className={tdClass}>
                      <span className={codeClass}>{t.PublicName}</span>
                    </td>
                    <td className={`${tdClass} font-mono text-[11px]`}>{t.UpstreamName}</td>
                    <td className={`${tdClass} max-w-80 truncate`} title={t.Description}>
                      {t.Description || <span className="text-muted-foreground">—</span>}
                    </td>
                    <td className={tdClass}>
                      {toggling === t.PublicName ? (
                        <Loader2 className="h-4 w-4 animate-spin text-muted-foreground" />
                      ) : (
                        <Checkbox
                          checked={granted}
                          onCheckedChange={(v) => void onToggle(t.PublicName, v === true)}
                          aria-label={`Grant ${t.PublicName}`}
                        />
                      )}
                    </td>
                  </tr>
                )
              })}
            </tbody>
          </table>
        )}
      </SettingsCard>
    </div>
  )
}
