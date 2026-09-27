import { useState } from 'react'
import { Loader2, UserRound } from 'lucide-react'
import { SettingsCard, SettingsCount } from '../../components/ui/SettingsCard'
import { Button } from '../../components/ui/Button'
import { Input } from '../../components/ui/Input'
import { createUser, listGroups, listMembers, listUsers } from './gatewayAdminApi'
import { ConsoleEmpty, ConsoleError, ConsoleLoading, ConsoleStale } from './gatewayConsoleShared'
import {
  codeClass,
  gatewayErrorMessage,
  plural,
  tableClass,
  tdClass,
  thClass,
  useAttempt,
  useGatewayLoader,
} from './gatewayConsoleUtils'

export function GatewayUsersPanel({ base }: { base: string }) {
  const [attempt, bump] = useAttempt()
  const { data, loading, error } = useGatewayLoader(async () => {
    const [users, groups] = await Promise.all([listUsers(base), listGroups(base)])
    const userGroups = new Map<string, string[]>()
    await Promise.all(
      groups.groups.map(async (g) => {
        for (const m of (await listMembers(base, g.ID)).members ?? []) {
          const list = userGroups.get(m) ?? []
          list.push(g.ID)
          userGroups.set(m, list)
        }
      }),
    )
    return { users: users.users, userGroups }
  }, attempt)

  const [userId, setUserId] = useState('')
  const [email, setEmail] = useState('')
  const [adding, setAdding] = useState(false)
  const [addError, setAddError] = useState<string | null>(null)

  async function onAdd() {
    if (!userId.trim()) {
      setAddError('Pick a user id.')
      return
    }
    setAdding(true)
    setAddError(null)
    try {
      await createUser(base, userId.trim(), email.trim())
      setUserId('')
      setEmail('')
      bump()
    } catch (err: unknown) {
      setAddError(gatewayErrorMessage(err))
    } finally {
      setAdding(false)
    }
  }

  if (loading) return <ConsoleLoading label="Loading users…" />
  if (!data) return <ConsoleError message={error ?? 'Failed to load.'} onRetry={bump} />

  return (
    <div className="space-y-4" data-testid="gateway-users">
      {error && <ConsoleStale message={error} onRetry={bump} />}
      <SettingsCard
        icon={<UserRound className="h-4 w-4 text-primary" />}
        title="Users"
        count={<SettingsCount>{plural(data.users.length, 'user')}</SettingsCount>}
        description="People who sign into MCP clients through the gateway. They get their tools from their groups."
      >
        {data.users.length === 0 ? (
          <ConsoleEmpty>No users yet. Create one below.</ConsoleEmpty>
        ) : (
          <div className="overflow-x-auto">
            <table className={tableClass}>
              <thead>
                <tr>
                  <th className={thClass}>User</th>
                  <th className={thClass}>Email</th>
                  <th className={thClass}>Groups</th>
                </tr>
              </thead>
              <tbody>
                {data.users.map((u) => (
                  <tr key={u.ID}>
                    <td className={tdClass}>
                      <span className={codeClass}>{u.ID}</span>
                    </td>
                    <td className={tdClass}>{u.Email}</td>
                    <td className={tdClass}>{(data.userGroups.get(u.ID) ?? []).join(', ') || <span className="text-muted-foreground">—</span>}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </SettingsCard>

      <SettingsCard title="Create a user" description="Ids are lowercase, e.g. alice.">
        <div className="grid grid-cols-2 gap-2">
          <Input aria-label="User id" placeholder="User id" value={userId} onChange={(e) => setUserId(e.target.value)} />
          <Input
            aria-label="Email"
            placeholder="Email"
            value={email}
            onChange={(e) => setEmail(e.target.value)}
            data-testid="gateway-user-email"
          />
        </div>
        {addError && (
          <p className="text-destructive" role="alert">
            {addError}
          </p>
        )}
        <div>
          <Button size="sm" disabled={adding} onClick={() => void onAdd()} data-testid="gateway-user-add-submit">
            {adding && <Loader2 className="animate-spin" />}
            Create user
          </Button>
        </div>
      </SettingsCard>
    </div>
  )
}
