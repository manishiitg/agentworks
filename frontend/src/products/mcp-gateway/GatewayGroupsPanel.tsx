import { useState } from 'react'
import { Loader2, UserMinus, UserPlus, UsersRound } from 'lucide-react'
import { SettingsCard, SettingsCount, SettingsEmpty } from '../../components/ui/SettingsCard'
import { Button } from '../../components/ui/Button'
import { Input } from '../../components/ui/Input'
import { addMember, createGroup, listGroups, listMembers, listUsers, removeMember } from './gatewayAdminApi'
import { ConsoleError, ConsoleLoading } from './gatewayConsoleShared'
import { gatewayErrorMessage, plural, useAttempt, useGatewayLoader } from './gatewayConsoleUtils'

const selectClass =
  'h-8 rounded-md border border-input bg-background px-2 text-xs shadow-sm focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring'

export function GatewayGroupsPanel({ base }: { base: string }) {
  const [attempt, bump] = useAttempt()
  const { data, loading, error } = useGatewayLoader(async () => {
    const [groups, users] = await Promise.all([listGroups(base), listUsers(base)])
    const members = new Map<string, string[]>()
    await Promise.all(
      groups.groups.map(async (g) => {
        members.set(g.ID, (await listMembers(base, g.ID)).members ?? [])
      }),
    )
    return { groups: groups.groups, users: users.users, members }
  }, attempt)

  const [groupId, setGroupId] = useState('')
  const [groupName, setGroupName] = useState('')
  const [adding, setAdding] = useState(false)
  const [addError, setAddError] = useState<string | null>(null)
  const [memberPick, setMemberPick] = useState<Record<string, string>>({})
  const [memberBusy, setMemberBusy] = useState(false)
  const [memberError, setMemberError] = useState<string | null>(null)

  async function onAdd() {
    if (!groupId.trim()) {
      setAddError('Pick a group id.')
      return
    }
    setAdding(true)
    setAddError(null)
    try {
      await createGroup(base, groupId.trim(), groupName.trim())
      setGroupId('')
      setGroupName('')
      bump()
    } catch (err: unknown) {
      setAddError(gatewayErrorMessage(err))
    } finally {
      setAdding(false)
    }
  }

  async function onAddMember(groupId: string) {
    const userId = memberPick[groupId]
    if (!userId) return
    setMemberBusy(true)
    setMemberError(null)
    try {
      await addMember(base, groupId, userId)
      bump()
    } catch (err: unknown) {
      setMemberError(gatewayErrorMessage(err))
    } finally {
      setMemberBusy(false)
    }
  }

  async function onRemoveMember(groupId: string, userId: string) {
    setMemberBusy(true)
    setMemberError(null)
    try {
      await removeMember(base, groupId, userId)
      bump()
    } catch (err: unknown) {
      setMemberError(gatewayErrorMessage(err))
    } finally {
      setMemberBusy(false)
    }
  }

  if (loading) return <ConsoleLoading label="Loading groups…" />
  if (error || !data) return <ConsoleError message={error ?? 'Failed to load.'} onRetry={bump} />

  return (
    <div className="space-y-4" data-testid="gateway-groups">
      <SettingsCard
        icon={<UsersRound className="h-4 w-4 text-primary" />}
        title="Groups"
        count={<SettingsCount>{plural(data.groups.length, 'group')}</SettingsCount>}
        description="Groups bundle users so one grant covers the whole team."
      >
        {memberError && <ConsoleError message={memberError} onRetry={bump} />}
        {data.groups.length === 0 ? (
          <SettingsEmpty>No groups yet. Create one below.</SettingsEmpty>
        ) : (
          <div className="space-y-3">
            {data.groups.map((g) => {
              const members = data.members.get(g.ID) ?? []
              const candidates = data.users.filter((u) => !members.includes(u.ID))
              return (
                <div key={g.ID} className="rounded-md border border-border/60 p-3">
                  <div className="flex flex-wrap items-center justify-between gap-2">
                    <p className="text-sm font-semibold text-foreground">
                      {g.Name || g.ID} <span className="font-mono text-[11px] font-normal text-muted-foreground">{g.ID}</span>
                    </p>
                    <span className="text-muted-foreground">{plural(members.length, 'member')}</span>
                  </div>
                  {members.length > 0 && (
                    <ul className="mt-2 flex flex-wrap gap-1.5">
                      {members.map((m) => (
                        <li
                          key={m}
                          className="inline-flex items-center gap-1 rounded-full bg-muted px-2 py-0.5 font-mono text-[11px]"
                        >
                          {m}
                          <button
                            onClick={() => void onRemoveMember(g.ID, m)}
                            disabled={memberBusy}
                            className="text-muted-foreground hover:text-destructive disabled:opacity-50"
                            aria-label={`Remove ${m} from ${g.ID}`}
                          >
                            <UserMinus className="h-3 w-3" />
                          </button>
                        </li>
                      ))}
                    </ul>
                  )}
                  {candidates.length > 0 && (
                    <div className="mt-2 flex items-center gap-2">
                      <select
                        aria-label={`Add member to ${g.ID}`}
                        className={selectClass}
                        value={memberPick[g.ID] ?? ''}
                        onChange={(e) => setMemberPick((prev) => ({ ...prev, [g.ID]: e.target.value }))}
                      >
                        <option value="">Add a member…</option>
                        {candidates.map((u) => (
                          <option key={u.ID} value={u.ID}>
                            {u.ID}
                          </option>
                        ))}
                      </select>
                      <Button variant="outline" size="xs" disabled={memberBusy || !memberPick[g.ID]} onClick={() => void onAddMember(g.ID)}>
                        <UserPlus />
                        Add
                      </Button>
                    </div>
                  )}
                </div>
              )
            })}
          </div>
        )}
      </SettingsCard>

      <SettingsCard title="Create a group" description="Ids are lowercase, e.g. eng or support.">
        <div className="grid grid-cols-2 gap-2">
          <Input aria-label="Group id" placeholder="Group id" value={groupId} onChange={(e) => setGroupId(e.target.value)} />
          <Input aria-label="Group name" placeholder="Display name" value={groupName} onChange={(e) => setGroupName(e.target.value)} />
        </div>
        {addError && (
          <p className="text-destructive" role="alert">
            {addError}
          </p>
        )}
        <div>
          <Button size="sm" disabled={adding} onClick={() => void onAdd()} data-testid="gateway-group-add-submit">
            {adding && <Loader2 className="animate-spin" />}
            Create group
          </Button>
        </div>
      </SettingsCard>
    </div>
  )
}
