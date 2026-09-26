import { useEffect, useMemo, useState } from 'react'
import { ChevronDown, KeyRound, Loader2, UserMinus, UserPlus, UsersRound } from 'lucide-react'
import { SettingsCard, SettingsCount, SettingsEmpty } from '../../components/ui/SettingsCard'
import { Button } from '../../components/ui/Button'
import { Checkbox } from '../../components/ui/checkbox'
import { Input } from '../../components/ui/Input'
import {
  addMember,
  attachGroupServer,
  createGroup,
  detachGroupServer,
  getGrants,
  listConnectors,
  listGroupServers,
  listGroups,
  listMembers,
  listTools,
  listUsers,
  removeMember,
  setGrant,
  type GatewayTool,
} from './gatewayAdminApi'
import { ConsoleError, ConsoleLoading } from './gatewayConsoleShared'
import { codeClass, gatewayErrorMessage, plural, useAttempt, useGatewayLoader } from './gatewayConsoleUtils'

const selectClass =
  'h-8 rounded-md border border-input bg-background px-2 text-xs shadow-sm focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring'

export function GatewayGroupsPanel({ base }: { base: string }) {
  const [attempt, bump] = useAttempt()
  const { data, loading, error } = useGatewayLoader(async () => {
    const [groups, users, connectors, tools] = await Promise.all([
      listGroups(base),
      listUsers(base),
      listConnectors(base),
      listTools(base),
    ])
    const members = new Map<string, string[]>()
    await Promise.all(
      groups.groups.map(async (g) => {
        members.set(g.ID, (await listMembers(base, g.ID)).members ?? [])
      }),
    )
    return { groups: groups.groups, users: users.users, connectors: connectors.connectors, tools: tools.tools, members }
  }, attempt)

  const [selectedId, setSelectedId] = useState<string | null>(null)
  const [groupId, setGroupId] = useState('')
  const [groupName, setGroupName] = useState('')
  const [createMembers, setCreateMembers] = useState<Set<string>>(new Set())
  const [adding, setAdding] = useState(false)
  const [addError, setAddError] = useState<string | null>(null)
  const [memberBusy, setMemberBusy] = useState(false)
  const [memberError, setMemberError] = useState<string | null>(null)
  const [memberPick, setMemberPick] = useState('')

  useEffect(() => {
    if (!data) return
    if (!data.groups.some((g) => g.ID === selectedId)) {
      setSelectedId(data.groups.length > 0 ? data.groups[0].ID : null)
    }
  }, [data, selectedId])

  const group = data?.groups.find((g) => g.ID === selectedId) ?? null

  async function onAdd() {
    if (!groupId.trim()) {
      setAddError('Pick a group id.')
      return
    }
    setAdding(true)
    setAddError(null)
    try {
      await createGroup(base, groupId.trim(), groupName.trim())
      for (const userId of createMembers) {
        await addMember(base, groupId.trim(), userId)
      }
      setGroupId('')
      setGroupName('')
      setCreateMembers(new Set())
      setSelectedId(groupId.trim())
      bump()
    } catch (err: unknown) {
      setAddError(gatewayErrorMessage(err))
    } finally {
      setAdding(false)
    }
  }

  async function onAddMember() {
    if (!group || !memberPick) return
    setMemberBusy(true)
    setMemberError(null)
    try {
      await addMember(base, group.ID, memberPick)
      setMemberPick('')
      bump()
    } catch (err: unknown) {
      setMemberError(gatewayErrorMessage(err))
    } finally {
      setMemberBusy(false)
    }
  }

  async function onRemoveMember(userId: string) {
    if (!group) return
    setMemberBusy(true)
    setMemberError(null)
    try {
      await removeMember(base, group.ID, userId)
      bump()
    } catch (err: unknown) {
      setMemberError(gatewayErrorMessage(err))
    } finally {
      setMemberBusy(false)
    }
  }

  if (loading) return <ConsoleLoading label="Loading groups…" />
  if (error || !data) return <ConsoleError message={error ?? 'Failed to load.'} onRetry={bump} />

  const members = group ? (data.members.get(group.ID) ?? []) : []
  const candidates = data.users.filter((u) => !members.includes(u.ID))

  return (
    <div className="space-y-4" data-testid="gateway-groups">
      <SettingsCard
        icon={<UsersRound className="h-4 w-4 text-primary" />}
        title="Groups"
        count={<SettingsCount>{plural(data.groups.length, 'group')}</SettingsCount>}
        description="A group is a set of users plus a set of permissions: whole MCP servers, specific tools, or both."
        actions={
          data.groups.length > 0 ? (
            <select
              aria-label="Selected group"
              className={selectClass}
              value={selectedId ?? ''}
              onChange={(e) => setSelectedId(e.target.value || null)}
              data-testid="gateway-group-select"
            >
              {data.groups.map((g) => (
                <option key={g.ID} value={g.ID}>
                  {g.Name || g.ID}
                </option>
              ))}
            </select>
          ) : undefined
        }
      >
        {!group ? (
          <SettingsEmpty>No groups yet. Create one below.</SettingsEmpty>
        ) : (
          <div>
            <p className="text-sm font-semibold text-foreground">
              {group.Name || group.ID}{' '}
              <span className="font-mono text-[11px] font-normal text-muted-foreground">{group.ID}</span>
            </p>
            {memberError && (
              <div className="mt-2">
                <ConsoleError message={memberError} onRetry={bump} />
              </div>
            )}
            <div className="mt-2">
              <p className="mb-1 text-muted-foreground">{plural(members.length, 'member')}</p>
              {members.length > 0 && (
                <ul className="flex flex-wrap gap-1.5">
                  {members.map((m) => (
                    <li key={m} className="inline-flex items-center gap-1 rounded-full bg-muted px-2 py-0.5 font-mono text-[11px]">
                      {m}
                      <button
                        onClick={() => void onRemoveMember(m)}
                        disabled={memberBusy}
                        className="text-muted-foreground hover:text-destructive disabled:opacity-50"
                        aria-label={`Remove ${m} from ${group.ID}`}
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
                    aria-label={`Add member to ${group.ID}`}
                    className={selectClass}
                    value={memberPick}
                    onChange={(e) => setMemberPick(e.target.value)}
                  >
                    <option value="">Add a member…</option>
                    {candidates.map((u) => (
                      <option key={u.ID} value={u.ID}>
                        {u.ID}
                      </option>
                    ))}
                  </select>
                  <Button variant="outline" size="xs" disabled={memberBusy || !memberPick} onClick={() => void onAddMember()}>
                    <UserPlus />
                    Add
                  </Button>
                </div>
              )}
            </div>
          </div>
        )}
      </SettingsCard>

      {group && (
        <GroupPermissions base={base} groupId={group.ID} groupName={group.Name || group.ID} attempt={attempt} onChanged={bump} />
      )}

      <SettingsCard title="Create a group" description="Ids are lowercase, e.g. eng. Pick the first members now; permissions come next.">
        <div className="grid grid-cols-2 gap-2">
          <Input aria-label="Group id" placeholder="Group id" value={groupId} onChange={(e) => setGroupId(e.target.value)} />
          <Input aria-label="Group name" placeholder="Display name" value={groupName} onChange={(e) => setGroupName(e.target.value)} />
        </div>
        {data.users.length > 0 && (
          <div>
            <p className="mb-1 text-muted-foreground">Members</p>
            <div className="flex flex-wrap gap-3">
              {data.users.map((u) => (
                <label key={u.ID} className="inline-flex cursor-pointer items-center gap-1.5 font-mono text-[11px]">
                  <Checkbox
                    checked={createMembers.has(u.ID)}
                    onCheckedChange={(v) =>
                      setCreateMembers((prev) => {
                        const next = new Set(prev)
                        if (v === true) next.add(u.ID)
                        else next.delete(u.ID)
                        return next
                      })
                    }
                    aria-label={`Include ${u.ID}`}
                  />
                  {u.ID}
                </label>
              ))}
            </div>
          </div>
        )}
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

function GroupPermissions({
  base,
  groupId,
  groupName,
  attempt,
  onChanged,
}: {
  base: string
  groupId: string
  groupName: string
  attempt: number
  onChanged: () => void
}) {
  const { data, loading, error } = useGatewayLoader(async () => {
    const [connectors, tools, servers, grants] = await Promise.all([
      listConnectors(base),
      listTools(base),
      listGroupServers(base, groupId),
      getGrants(base, { group: groupId }),
    ])
    return {
      connectors: connectors.connectors,
      tools: tools.tools,
      servers: new Set(servers.servers ?? []),
      grants: new Set(grants ?? []),
    }
  }, attempt)
  const [busy, setBusy] = useState<string | null>(null)
  const [permError, setPermError] = useState<string | null>(null)
  const [expanded, setExpanded] = useState<Set<string>>(new Set())

  const toolsByConnector = useMemo(() => {
    const byId = new Map<string, GatewayTool[]>()
    for (const t of data?.tools ?? []) {
      const list = byId.get(t.ConnectorID) ?? []
      list.push(t)
      byId.set(t.ConnectorID, list)
    }
    for (const list of byId.values()) list.sort((a, b) => a.PublicName.localeCompare(b.PublicName))
    return byId
  }, [data])

  // Reset transient UI when switching groups (attempt also changes via parent bump).
  useEffect(() => {
    setPermError(null)
    setExpanded(new Set())
  }, [groupId])

  async function run(key: string, fn: () => Promise<unknown>) {
    setBusy(key)
    setPermError(null)
    try {
      await fn()
      onChanged()
    } catch (err: unknown) {
      setPermError(gatewayErrorMessage(err))
    } finally {
      setBusy(null)
    }
  }

  if (loading) return <ConsoleLoading label="Loading permissions…" />
  if (error || !data) {
    return (
      <SettingsCard
        icon={<KeyRound className="h-4 w-4 text-primary" />}
        title={`Permissions for ${groupName}`}
        description="Attach whole MCP servers for full access, or grant specific tools."
      >
        <ConsoleError message={error ?? 'Failed to load.'} onRetry={onChanged} />
      </SettingsCard>
    )
  }

  return (
    <SettingsCard
      icon={<KeyRound className="h-4 w-4 text-primary" />}
      title={`Permissions for ${groupName}`}
      count={<SettingsCount>{`${data.servers.size} servers · ${data.grants.size} tools`}</SettingsCount>}
      description="Attach whole MCP servers for full access (covers tools discovered later), or grant specific tools."
    >
      {permError && <ConsoleError message={permError} onRetry={onChanged} />}
      {data.connectors.length === 0 ? (
        <SettingsEmpty>No servers connected yet. Connect one on the Servers page first.</SettingsEmpty>
      ) : (
        <div className="space-y-2" data-testid="gateway-permissions">
          {data.connectors.map((c) => {
            const full = data.servers.has(c.ID)
            const tools = toolsByConnector.get(c.ID) ?? []
            const open = expanded.has(c.ID)
            return (
              <div key={c.ID} className="rounded-md border border-border/60 p-3">
                <div className="flex flex-wrap items-center gap-x-3 gap-y-1">
                  <button
                    onClick={() =>
                      setExpanded((prev) => {
                        const next = new Set(prev)
                        if (next.has(c.ID)) next.delete(c.ID)
                        else next.add(c.ID)
                        return next
                      })
                    }
                    aria-expanded={open}
                    aria-label={`${open ? 'Hide' : 'Show'} tools on ${c.Label || c.Provider}`}
                    className="inline-flex items-center gap-1.5 rounded font-medium text-foreground hover:bg-muted/60"
                  >
                    <ChevronDown className={`h-3.5 w-3.5 text-muted-foreground transition-transform ${open ? 'rotate-180' : ''}`} aria-hidden />
                    {c.Label || c.Provider}
                    <span className="font-mono text-[11px] font-normal text-muted-foreground">
                      {plural(tools.length, 'tool')}
                    </span>
                  </button>
                  <label className="ml-auto inline-flex cursor-pointer items-center gap-1.5 text-xs">
                    {busy === `full:${c.ID}` ? (
                      <Loader2 className="h-4 w-4 animate-spin text-muted-foreground" />
                    ) : (
                      <Checkbox
                        checked={full}
                        onCheckedChange={(v) =>
                          void run(`full:${c.ID}`, () =>
                            v === true ? attachGroupServer(base, groupId, c.ID) : detachGroupServer(base, groupId, c.ID),
                          )
                        }
                        aria-label={`Full access to ${c.Label || c.Provider}`}
                      />
                    )}
                    Full access
                  </label>
                </div>
                {open && (
                  <div className="mt-2 space-y-1 border-t border-border/60 pt-2">
                    {tools.length === 0 && <p className="text-muted-foreground">No tools discovered yet.</p>}
                    {tools.map((t) => {
                      const granted = full || data.grants.has(t.PublicName)
                      return (
                        <label key={t.PublicName} className="flex cursor-pointer items-start gap-2">
                          {busy === `tool:${t.PublicName}` ? (
                            <Loader2 className="mt-0.5 h-4 w-4 animate-spin text-muted-foreground" />
                          ) : (
                            <Checkbox
                              checked={granted}
                              disabled={full}
                              onCheckedChange={(v) =>
                                void run(`tool:${t.PublicName}`, () => setGrant(base, { group: groupId }, t.PublicName, v === true))
                              }
                              aria-label={`Grant ${t.PublicName}`}
                              className="mt-0.5"
                            />
                          )}
                          <span className="min-w-0">
                            <span className={codeClass}>{t.PublicName}</span>
                            {t.Description && (
                              <span className="block truncate text-muted-foreground" title={t.Description}>
                                {t.Description.split('\n')[0]}
                              </span>
                            )}
                          </span>
                        </label>
                      )
                    })}
                  </div>
                )}
              </div>
            )
          })}
        </div>
      )}
    </SettingsCard>
  )
}
