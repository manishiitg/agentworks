import { useEffect, useMemo, useRef, useState } from 'react'
import { Check, ChevronDown, Copy, KeyRound, Loader2, Pencil, UserMinus, UserPlus, UserRound, UsersRound, X } from 'lucide-react'
import { SettingsCard, SettingsCount, SettingsEmpty } from '../../components/ui/SettingsCard'
import { Button } from '../../components/ui/Button'
import { Checkbox } from '../../components/ui/checkbox'
import { Input } from '../../components/ui/Input'
import { WorkspaceViewTabs } from '../../components/workflow/WorkspaceViewTabs'
import { GatewayGroupPolicyReview } from './GatewayGroupPolicyReview'
import { GatewayToolCard } from './GatewayToolCard'
import ConfirmationDialog from '../../components/ui/ConfirmationDialog'
import {
  addMember,
  createGroup,
  createGroupKey,
  listConnectors,
  listGroupKeys,
  listGroupPermissions,
  listAccessPackages,
  listGroupServers,
  listGroups,
  listMembers,
  listTools,
  listUsers,
  removeMember,
  removeGroupServerAccess,
  renameGroup,
  revokeGroupKey,
  setGrant,
  type GatewayAPIKey,
  type GatewayTool,
} from './gatewayAdminApi'
import { ConsoleError, ConsoleLoading, ConsoleStale } from './gatewayConsoleShared'
import {
  codeClass,
  formatDateTime,
  gatewayErrorMessage,
  plural,
  slugifyId,
  useAttempt,
  useGatewayLoader,
} from './gatewayConsoleUtils'

const selectClass =
  'h-8 rounded-md border border-input bg-background px-2 text-xs shadow-sm focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring'

export function GatewayGroupsPanel({ base, revision, chatBusy = false, directoryOnly = false }: {
  base: string; revision?: string; chatBusy?: boolean; directoryOnly?: boolean
}) {
  const [attempt, bump] = useAttempt()
  const { data, loading, error } = useGatewayLoader(async () => {
    const [groups, users] = await Promise.all([listGroups(base), listUsers(base)])
    return { groups: groups.groups, users: users.users }
  }, attempt)

  const lastRevision = useRef(revision)
  useEffect(() => {
    if (revision && revision !== lastRevision.current) { lastRevision.current = revision; bump() }
  }, [revision, bump])
  const [detailTab, setDetailTab] = useState<'users' | 'permissions'>('permissions')
  const [showCreate, setShowCreate] = useState(false)
  const [showKeys, setShowKeys] = useState(false)
  const [selectedId, setSelectedId] = useState<string | null>(null)
  const [groupSearch, setGroupSearch] = useState('')
  const [groupName, setGroupName] = useState('')
  const [createMembers, setCreateMembers] = useState<Set<string>>(new Set())
  const [adding, setAdding] = useState(false)
  const [addError, setAddError] = useState<string | null>(null)
  const [memberBusy, setMemberBusy] = useState(false)
  const [memberError, setMemberError] = useState<string | null>(null)
  const [memberPick, setMemberPick] = useState('')
  const [renaming, setRenaming] = useState(false)
  const [renameDraft, setRenameDraft] = useState('')
  const [renameBusy, setRenameBusy] = useState(false)
  const [renameError, setRenameError] = useState<string | null>(null)

  useEffect(() => {
    if (!data) return
    if (!data.groups.some((g) => g.ID === selectedId)) {
      setSelectedId(data.groups.length > 0 ? data.groups[0].ID : null)
    }
  }, [data, selectedId])

  useEffect(() => {
    setRenaming(false)
    setRenameError(null)
    setShowKeys(false)
    setMemberPick('')
  }, [selectedId])

  const [memberData, setMemberData] = useState<{ groupId: string; members: string[] } | null>(null)
  const [membersLoading, setMembersLoading] = useState(false)
  useEffect(() => {
    if (!selectedId) { setMemberData(null); return }
    let cancelled = false
    setMembersLoading(true); setMemberError(null)
    listMembers(base, selectedId).then(result => {
      if (!cancelled) setMemberData({ groupId: selectedId, members: result.members ?? [] })
    }).catch(err => { if (!cancelled) setMemberError(gatewayErrorMessage(err)) })
      .finally(() => { if (!cancelled) setMembersLoading(false) })
    return () => { cancelled = true }
  }, [base, selectedId, attempt])

  const group = data?.groups.find((g) => g.ID === selectedId) ?? null

  async function onAdd() {
    const name = groupName.trim()
    const id = slugifyId(name)
    if (!id) {
      setAddError('Pick a name with letters or numbers.')
      return
    }
    setAdding(true)
    setAddError(null)
    try {
      await createGroup(base, id, name)
      for (const userId of createMembers) {
        await addMember(base, id, userId)
      }
      setShowCreate(false)
      setGroupName('')
      setCreateMembers(new Set())
      setSelectedId(id)
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

  async function onRename() {
    if (!group || !renameDraft.trim()) {
      setRenameError('Pick a name.')
      return
    }
    setRenameBusy(true)
    setRenameError(null)
    try {
      await renameGroup(base, group.ID, renameDraft.trim())
      setRenaming(false)
      bump()
    } catch (err: unknown) {
      setRenameError(gatewayErrorMessage(err))
    } finally {
      setRenameBusy(false)
    }
  }

  if (loading) return <ConsoleLoading label="Loading groups…" />
  if (!data) return <ConsoleError message={error ?? 'Failed to load.'} onRetry={bump} />

  const members = group && memberData?.groupId === group.ID ? memberData.members : []
  const candidates = data.users.filter((u) => !members.includes(u.ID))
  const derivedId = slugifyId(groupName)

  return (
    <div className="space-y-4" data-testid="gateway-groups">
      {error && <ConsoleStale message={error} onRetry={bump} />}
      <SettingsCard
        icon={<UsersRound className="h-4 w-4 text-primary" />}
        title="Groups"
        count={<SettingsCount>{plural(data.groups.length, 'group')}</SettingsCount>}
        description={directoryOnly ? 'Manage members here. Set permissions in Access.' : undefined}
        actions={
          directoryOnly ? <Button variant="outline" size="xs" onClick={() => setShowCreate(v => !v)}>New group</Button> : undefined
        }
      >
        {data.groups.length > 0 && <div className="space-y-2" data-testid="gateway-group-select" aria-label="Choose a group">
          {data.groups.length > 1 && <Input aria-label="Find a group" placeholder="Find a group…" value={groupSearch} onChange={event => setGroupSearch(event.target.value)} />}
          <div className="flex max-h-44 flex-col gap-1.5 overflow-y-auto">
            {data.groups.filter(g => (g.Name || g.ID).toLowerCase().includes(groupSearch.trim().toLowerCase())).map(g => <Button
              key={g.ID} variant="outline" size="sm" aria-label={`Select group ${g.Name || g.ID}`} aria-pressed={g.ID === selectedId}
              onClick={() => setSelectedId(g.ID)}
              className={`h-auto min-h-10 w-full justify-start whitespace-normal py-2 text-left ${g.ID === selectedId ? 'border-primary/50 bg-primary/10 text-foreground hover:bg-primary/15' : 'text-muted-foreground'}`}
            >
              <UsersRound className={g.ID === selectedId ? 'shrink-0 text-primary' : 'shrink-0'} />
              <span className="min-w-0 flex-1 break-words">{g.Name || g.ID}</span>
              {g.ID === selectedId && <Check className="shrink-0 text-primary" aria-hidden />}
            </Button>)}
            {!data.groups.some(g => (g.Name || g.ID).toLowerCase().includes(groupSearch.trim().toLowerCase())) && <SettingsEmpty>No groups match your search.</SettingsEmpty>}
          </div>
        </div>}
        {!group ? (
          <SettingsEmpty>{directoryOnly ? 'No groups yet. Create one below.' : 'No groups available. Manage groups under People → Groups.'}</SettingsEmpty>
        ) : (
          <div className="mt-4 border-t border-border pt-4">
            {renaming ? (
              <div className="flex flex-wrap items-center gap-2">
                <Input
                  aria-label="Group name"
                  value={renameDraft}
                  onChange={(e) => setRenameDraft(e.target.value)}
                  className="max-w-xs"
                  data-testid="gateway-group-rename-input"
                />
                <Button size="xs" disabled={renameBusy} onClick={() => void onRename()} aria-label="Save group name">
                  {renameBusy ? <Loader2 className="animate-spin" /> : <Check />}
                  Save
                </Button>
                <Button variant="ghost" size="xs" onClick={() => setRenaming(false)} aria-label="Cancel rename">
                  <X />
                </Button>
                <span className="font-mono text-[11px] text-muted-foreground">id: {group.ID} (never changes)</span>
              </div>
            ) : (
              <p className="flex flex-wrap items-center gap-2 text-sm font-semibold text-foreground">
                {group.Name || group.ID}
                {directoryOnly && <span className="font-mono text-[11px] font-normal text-muted-foreground">{group.ID}</span>}
                {directoryOnly && <Button
                  variant="ghost"
                  size="xs"
                  onClick={() => {
                    setRenameDraft(group.Name || group.ID)
                    setRenameError(null)
                    setRenaming(true)
                  }}
                  aria-label="Rename group"
                >
                  <Pencil />
                </Button>}
              </p>
            )}
            {renameError && (
              <p className="mt-1 text-destructive" role="alert">
                {renameError}
              </p>
            )}
            {memberError && <ConsoleError message={memberError} onRetry={bump} />}
            {!directoryOnly && <div className="mt-3 border-b border-border">
              <WorkspaceViewTabs value={detailTab} onChange={value => setDetailTab(value === 'users' ? 'users' : 'permissions')}
                options={[{ value: 'users', label: 'Users', icon: UserRound, count: members.length }, { value: 'permissions', label: 'Permissions', icon: KeyRound }]}
                ariaLabel="Group tabs" />
            </div>}
            {(directoryOnly || detailTab === 'users') && <div className="mt-3" role="tabpanel" aria-label="Group users">
              {membersLoading && <ConsoleLoading label="Loading group users…" />}
              <p className="mb-1 text-muted-foreground">{plural(members.length, 'member')}</p>
              {members.length > 0 && (
                <ul className="flex flex-wrap gap-1.5">
                  {members.map((m) => (
                    <li key={m} className="inline-flex items-center gap-1 rounded-full bg-muted px-2 py-0.5 font-mono text-[11px]">
                      {data.users.find(u => u.ID === m)?.Email || m}
                      {directoryOnly && <button
                        onClick={() => void onRemoveMember(m)}
                        disabled={memberBusy || membersLoading}
                        className="text-muted-foreground hover:text-destructive disabled:opacity-50"
                        aria-label={`Remove ${m} from ${group.ID}`}
                      >
                        <UserMinus className="h-3 w-3" />
                      </button>}
                    </li>
                  ))}
                </ul>
              )}
              {directoryOnly && candidates.length > 0 && (
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
                        {u.Email || u.ID}
                      </option>
                    ))}
                  </select>
                  <Button variant="outline" size="xs" disabled={memberBusy || membersLoading || !memberPick} onClick={() => void onAddMember()}>
                    <UserPlus />
                    Add
                  </Button>
                </div>
              )}
              {directoryOnly && <Button variant="ghost" size="xs" className="mt-3" onClick={() => setShowKeys(v => !v)} aria-expanded={showKeys}>Group API keys</Button>}
              {showKeys && <div className="mt-3"><GroupAPIKeys key={group.ID} base={base} groupId={group.ID} groupName={group.Name || group.ID} attempt={attempt} onChanged={bump} /></div>}
            </div>}
            {!directoryOnly && detailTab === 'permissions' && <div role="tabpanel" aria-label="Group permissions" className="mt-4 space-y-6">
              <GroupPermissions key={group.ID} base={base} groupId={group.ID} groupName={group.Name || group.ID} attempt={attempt} onChanged={bump} />
              <GatewayGroupPolicyReview key={`policies:${group.ID}`} base={base} groupId={group.ID} revision={`${revision ?? ''}:${attempt}`} onChanged={bump} chatBusy={chatBusy} />
            </div>}
          </div>
        )}
      </SettingsCard>

      {directoryOnly && (showCreate || data.groups.length === 0) && <SettingsCard title="Create a group" description="Name your group and choose members.">
        <div>
          <Input
            aria-label="New group name"
            placeholder="Group name (e.g. Support engineers)"
            value={groupName}
            onChange={(e) => setGroupName(e.target.value)}
            className="max-w-md"
            data-testid="gateway-group-name-input"
          />
          {derivedId && <p className="mt-1 text-muted-foreground">id: <span className={codeClass}>{derivedId}</span></p>}
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
      </SettingsCard>}
    </div>
  )
}

function GroupAPIKeys({
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
  const { data, loading, error } = useGatewayLoader(async () => listGroupKeys(base, groupId), attempt)
  const [label, setLabel] = useState('')
  const [creating, setCreating] = useState(false)
  const [keyError, setKeyError] = useState<string | null>(null)
  const [freshKey, setFreshKey] = useState<GatewayAPIKey | null>(null)
  const [copied, setCopied] = useState(false)
  const [revoking, setRevoking] = useState<GatewayAPIKey | null>(null)
  const [revokeBusy, setRevokeBusy] = useState(false)

  useEffect(() => {
    setFreshKey(null)
    setCopied(false)
    setKeyError(null)
  }, [groupId])

  async function onCreate() {
    setCreating(true)
    setKeyError(null)
    setFreshKey(null)
    setCopied(false)
    try {
      const key = await createGroupKey(base, groupId, label.trim())
      setLabel('')
      setFreshKey(key)
      onChanged()
    } catch (err: unknown) {
      setKeyError(gatewayErrorMessage(err))
    } finally {
      setCreating(false)
    }
  }

  async function onRevoke() {
    if (!revoking) return
    setRevokeBusy(true)
    try {
      await revokeGroupKey(base, groupId, revoking.ID)
      setRevoking(null)
      onChanged()
    } catch (err: unknown) {
      setKeyError(gatewayErrorMessage(err))
      setRevoking(null)
    } finally {
      setRevokeBusy(false)
    }
  }

  function onCopy() {
    if (!freshKey) return
    try {
      void navigator.clipboard?.writeText(freshKey.Token)
    } catch {
      // Clipboard unavailable (permissions); the token stays visible for manual copy.
    }
    setCopied(true)
  }

  return (
    <SettingsCard
      icon={<KeyRound className="h-4 w-4 text-primary" />}
      title={`API keys for ${groupName}`}
      count={data ? <SettingsCount>{plural(data.keys.length, 'key')}</SettingsCount> : undefined}
      description="Keys use this group's permissions."
    >
      {loading ? (
        <ConsoleLoading label="Loading keys…" />
      ) : !data ? (
        <ConsoleError message={error ?? 'Failed to load.'} onRetry={onChanged} />
      ) : (
        <>
          {error && <ConsoleStale message={error} onRetry={onChanged} />}
          {data.keys.length > 0 && (
            <ul className="space-y-1.5" data-testid="gateway-keys">
              {data.keys.map((k) => (
                <li key={k.ID} className="flex flex-wrap items-center gap-x-3 gap-y-1 rounded-md border border-border/60 px-3 py-2">
                  <span className="font-medium text-foreground">{k.Label || 'Unlabeled key'}</span>
                  <span className={codeClass}>{k.ID}</span>
                  <span className="text-muted-foreground">created {formatDateTime(k.CreatedAt)}</span>
                  <span className="text-muted-foreground">last used {formatDateTime(k.LastUsedAt)}</span>
                  <Button
                    variant="ghost"
                    size="xs"
                    className="ml-auto"
                    onClick={() => setRevoking(k)}
                    aria-label={`Revoke ${k.Label || k.ID}`}
                  >
                    Revoke
                  </Button>
                </li>
              ))}
            </ul>
          )}
          <div className="flex flex-wrap items-center gap-2">
            <Input
              aria-label="Key label"
              placeholder="Label (e.g. contractor laptop)"
              value={label}
              onChange={(e) => setLabel(e.target.value)}
              className="max-w-xs"
              data-testid="gateway-key-label"
            />
            <Button size="sm" disabled={creating} onClick={() => void onCreate()} data-testid="gateway-key-create">
              {creating && <Loader2 className="animate-spin" />}
              Create key
            </Button>
          </div>
        </>
      )}
      {keyError && (
        <p className="text-destructive" role="alert">
          {keyError}
        </p>
      )}
      {freshKey && (
        <div className="space-y-2 rounded-md border border-amber-500/40 bg-amber-500/5 p-3" role="status" data-testid="gateway-key-fresh">
          <p className="text-sm font-medium text-foreground">Copy this key now — it is shown once.</p>
          <p className="flex flex-wrap items-center gap-2">
            <code className="break-all rounded bg-muted px-2 py-1 font-mono text-[11px]">{freshKey.Token}</code>
            <Button variant="outline" size="xs" onClick={onCopy}>
              {copied ? <Check /> : <Copy />}
              {copied ? 'Copied' : 'Copy'}
            </Button>
          </p>
          <p className="text-muted-foreground">
            Use as <span className={codeClass}>Authorization: Bearer &lt;key&gt;</span> against{' '}
            <span className={codeClass}>{`${base}/mcp`}</span>
          </p>
        </div>
      )}
      <ConfirmationDialog
        isOpen={revoking !== null}
        onClose={() => setRevoking(null)}
        onConfirm={() => void onRevoke()}
        title="Revoke API key"
        message={`Revoke "${revoking?.Label || revoking?.ID}"? Anything using it loses access immediately.`}
        confirmText="Revoke"
        isLoading={revokeBusy}
      />
    </SettingsCard>
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
    const [connectors, tools, servers, permissions, policies] = await Promise.all([
      listConnectors(base),
      listTools(base),
      listGroupServers(base, groupId),
      listGroupPermissions(base, groupId),
      listAccessPackages(base),
    ])
    return {
      connectors: connectors.connectors,
      tools: tools.tools,
      servers: new Set(servers.servers ?? []),
      permissions: new Map(permissions.permissions.map(p => [p.public_name, p])),
      policies: policies.packages.filter(p => p.group_id === groupId),
    }
  }, attempt)
  const [busy, setBusy] = useState<string | null>(null)
  const [permError, setPermError] = useState<string | null>(null)
  const [expanded, setExpanded] = useState<Set<string>>(new Set())
  const [removeServer, setRemoveServer] = useState<{ id: string; name: string } | null>(null)

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
      return true
    } catch (err: unknown) {
      setPermError(gatewayErrorMessage(err))
      return false
    } finally {
      setBusy(null)
    }
  }

  if (loading) return <ConsoleLoading label="Loading permissions…" />
  if (!data) {
    return (
      <SettingsCard
        icon={<KeyRound className="h-4 w-4 text-primary" />}
        title="Permissions"
        className="border-0 p-0 rounded-none"
      >
        <ConsoleError message={error ?? 'Failed to load.'} onRetry={onChanged} />
      </SettingsCard>
    )
  }

  return (
    <SettingsCard
      icon={<KeyRound className="h-4 w-4 text-primary" />}
      title="Permissions"
      className="border-0 p-0 rounded-none"
      count={<SettingsCount>{`${data.connectors.length} MCPs · ${[...data.permissions.values()].filter(p => p.allowed).length} tools allowed`}</SettingsCount>}

    >
      {permError && <ConsoleError message={permError} onRetry={onChanged} />}
      {error && <ConsoleStale message={error} onRetry={onChanged} />}
      {data.connectors.length === 0 ? (
        <SettingsEmpty>No servers connected yet. Connect one on the Servers page first.</SettingsEmpty>
      ) : (
        <div className="space-y-2" data-testid="gateway-permissions">
          {data.connectors.map((c) => {
            const full = data.servers.has(c.ID)
            const tools = toolsByConnector.get(c.ID) ?? []
            const assigned = full || tools.some(t => data.permissions.get(t.PublicName)?.assigned || data.permissions.get(t.PublicName)?.allowed || data.permissions.get(t.PublicName)?.source === 'tool')
              || data.policies.some(p => (p.status === 'draft' || p.status === 'published') && p.rules.some(r => tools.some(t => t.PublicName === r.public_name)))
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
                  {full && <span className="text-xs text-muted-foreground">Server-wide grant active</span>}
                  <Button variant="outline" size="xs" disabled={!assigned || busy !== null}
                    className="ml-auto text-destructive hover:text-destructive"
                    aria-label={`Remove ${c.Label || c.Provider} from group`}
                    onClick={() => setRemoveServer({ id: c.ID, name: c.Label || c.Provider })}>
                    Remove from group
                  </Button>
                </div>
                {open && (
                  <div className="mt-2 space-y-1 border-t border-border/60 pt-2">
                    {tools.length === 0 && <p className="text-muted-foreground">No tools discovered yet.</p>}
                    {tools.map((t) => {
                      const effective = data.permissions.get(t.PublicName)
                      const governed = effective?.governed ?? false
                      const granted = effective?.allowed ?? false
                      const policies = data.policies.filter(p => p.rules.some(r => r.public_name === t.PublicName))
                      const currentRules = policies.filter(p => p.status === 'published').flatMap(p => p.rules.filter(r => r.public_name === t.PublicName))
                      const restricted = granted && currentRules.length > 0 && currentRules.every(r => r.conditions.length > 0)
                      return (
                        <GatewayToolCard key={t.PublicName} name={t.UpstreamName} description={t.Description} schema={t.InputSchema}
                          status={t.Status !== 'active' ? 'Unavailable — tool needs approval' : granted ? restricted ? 'Allowed with restrictions' : 'Allowed' : 'No access'}
                          selection={<Checkbox checked={granted} disabled={full || governed || busy !== null || t.Status !== 'active'}
                            onCheckedChange={v => void run(`tool:${t.PublicName}`, () => setGrant(base, { group: groupId }, t.PublicName, v === true))}
                            aria-label={`Grant ${t.PublicName}`} className="mt-0.5" />}>
                          {policies.length > 0 && <div className="mt-3 space-y-3 border-t border-border pt-3" aria-label={`${t.UpstreamName} permission details`}>
                            <p className="text-muted-foreground">{t.Status !== 'active' ? 'This tool is unavailable until its definition is approved.'
                              : governed ? granted ? restricted ? 'Access is limited by the published conditions shown below.' : 'Assigned to this group by a published permission policy.' : 'No published policy grants this group access. Describe the permissions you want for this tool in chat.'
                                : full ? 'Assigned to this group through an existing server grant.'
                                  : granted ? 'Assigned to this group. Members can use this tool with any valid arguments.'
                                    : 'Not assigned to this group. Select the checkbox beside the tool to give access, or ask AI to set advanced permissions.'}</p>
                            {policies.map(p => <div key={`${p.id}:${p.status}`} className="space-y-1 rounded border border-border p-2">
                              <p className="font-medium">{p.name} · {p.status === 'draft' ? 'Draft — not active' : p.status === 'published' ? 'Published' : 'Revoked'}</p>
                              {p.rules.filter(r => r.public_name === t.PublicName).map(rule => <div key={rule.public_name}>
                                {rule.conditions.length === 0 ? <p className="text-muted-foreground">All arguments permitted</p> : rule.conditions.map((condition, i) => <p key={i} className="break-all text-muted-foreground">{condition.path.replace(/^\//, '')} {condition.op === 'equals' ? 'must equal' : 'must match'} <span className="font-mono text-foreground">{condition.value}</span></p>)}
                                {p.status === 'published' && rule.fingerprint !== t.Fingerprint && <p className="text-amber-600">Tool changed — this rule must be updated before it can grant access.</p>}
                              </div>)}
                            </div>)}
                            {currentRules.length > 1 && <p className="text-muted-foreground">A call is allowed when it satisfies any one published rule. All conditions in that rule must match.</p>}

                          </div>}
                        </GatewayToolCard>
                      )
                    })}
                  </div>
                )}
              </div>
            )
          })}
        </div>
      )}
      <ConfirmationDialog
        isOpen={removeServer !== null}
        onClose={() => setRemoveServer(null)}
        onConfirm={() => {
          if (!removeServer) return
          const serverId = removeServer.id
          void run(`remove:${serverId}`, () => removeGroupServerAccess(base, groupId, serverId)).then(success => { if (success) setRemoveServer(null) })
        }}
        title={`Remove ${removeServer?.name ?? 'server'} from ${groupName}?`}
        message="Remove this group’s whole-server grant, individual tool grants, and this server’s rules from published permissions and drafts. Permissions for other servers and groups are preserved. The server stays connected."
        confirmText="Remove from group"
        type="danger"
        loadingText="Removing…"
        isLoading={busy === `remove:${removeServer?.id}`}
      />
    </SettingsCard>
  )
}
