import { SecretSelectionSection } from '../../components/secrets/SecretSelectionSection'
import { secretsApi } from '../../api/secrets'
import { useEffect, useMemo, useRef, useState } from 'react'
import { ArrowLeft, Check, ChevronRight, Copy, KeyRound, Loader2, LockKeyhole, Server, UserMinus, UserPlus, UserRound, UsersRound, X } from 'lucide-react'
import { SettingsCard, SettingsCount, SettingsEmpty } from '../../components/ui/SettingsCard'
import { Button } from '../../components/ui/Button'
import { Checkbox } from '../../components/ui/checkbox'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '../../components/ui/select'
import { Input } from '../../components/ui/Input'
import { Textarea } from '../../components/ui/Textarea'
import { WorkspaceViewTabs } from '../../components/workflow/WorkspaceViewTabs'
import { McpServerHeader } from '../../components/integrations/McpServerHeader'
import { McpToolCard } from '../../components/integrations/McpToolCard'
import ConfirmationDialog from '../../components/ui/ConfirmationDialog'
import {
  addMember,
  attachGroupServer,
  createGroup,
  createGroupKey,
  listConnectors,
  listGroupKeys,
  listGroupPermissions,
  listGroupSecrets,
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
import { useVaultReadOnly } from './vaultReadOnly'
import {
  codeClass,
  formatDateTime,
  gatewayErrorMessage,
  plural,
  slugifyId,
  useAttempt,
  useGatewayLoader,
} from './gatewayConsoleUtils'

const memberLabel = (id: string, email?: string) => email || (id === 'default' ? 'Local account' : id)

export function GatewayGroupsPanel({ base, revision, chatBusy = false, directoryOnly = false, allowLegacyAPIKeys = false }: {
  base: string; revision?: string; chatBusy?: boolean; directoryOnly?: boolean; allowLegacyAPIKeys?: boolean
}) {
  const readOnly = useVaultReadOnly()
  const [attempt, bump] = useAttempt()
  const { data, loading, error } = useGatewayLoader(async () => {
    const [groups, users] = await Promise.all([listGroups(base), listUsers(base)])
    return { groups: groups.groups, users: users.users }
  }, attempt)

  const lastRevision = useRef(revision)
  useEffect(() => {
    if (revision && revision !== lastRevision.current) { lastRevision.current = revision; bump() }
  }, [revision, bump])
  const [detailTab, setDetailTab] = useState<'users' | 'mcps' | 'secrets'>('mcps')
  const [showCreate, setShowCreate] = useState(false)
  const [showKeys, setShowKeys] = useState(false)
  const [selectedId, setSelectedId] = useState<string | null>(null)
  const [groupName, setGroupName] = useState('')
  const [groupDescription, setGroupDescription] = useState('')
  const [createMembers, setCreateMembers] = useState<Set<string>>(new Set())
  const [adding, setAdding] = useState(false)
  const [addError, setAddError] = useState<string | null>(null)
  const [memberBusy, setMemberBusy] = useState(false)
  const [memberError, setMemberError] = useState<string | null>(null)
  const [memberPick, setMemberPick] = useState('')
  const [renameDraft, setRenameDraft] = useState('')
  const [descriptionDraft, setDescriptionDraft] = useState('')
  const [renameBusy, setRenameBusy] = useState(false)
  const [renameError, setRenameError] = useState<string | null>(null)

  useEffect(() => {
    if (!data) return
    if (selectedId && !data.groups.some((g) => g.ID === selectedId)) {
      setSelectedId(null)
    }
  }, [data, selectedId])

  useEffect(() => {
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
  useEffect(() => {
    setRenameDraft(group?.Name || group?.ID || '')
    setDescriptionDraft(group?.Description || '')
  }, [group?.ID, group?.Name, group?.Description])

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
      await createGroup(base, id, name, groupDescription.trim())
      for (const userId of createMembers) {
        await addMember(base, id, userId)
      }
      setShowCreate(false)
      setGroupName('')
      setGroupDescription('')
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
      await renameGroup(base, group.ID, renameDraft.trim(), descriptionDraft.trim())
        bump()
    } catch (err: unknown) {
      setRenameError(gatewayErrorMessage(err))
    } finally {
      setRenameBusy(false)
    }
  }

  if (loading) return <ConsoleLoading label="Loading groups…" />
  if (!data) return <ConsoleError message={error ?? 'Failed to load.'} onRetry={bump} />

  const members = group?.BuiltIn ? data.users.map(user => user.ID) : group && memberData?.groupId === group.ID ? memberData.members : []
  const candidates = data.users.filter((u) => !members.includes(u.ID))
  const derivedId = slugifyId(groupName)

  return (
    <div className="space-y-4" data-testid="gateway-groups">
      {error && <ConsoleStale message={error} onRetry={bump} />}
      {!showCreate && <>{!group && <SettingsCard
        icon={<UsersRound className="h-4 w-4 text-primary" />}
        title="Access groups"
        count={<SettingsCount>{plural(data.groups.length, 'group')}</SettingsCount>}
        actions={
          directoryOnly && !readOnly ? <Button variant="outline" size="xs" onClick={() => { setAddError(null); setShowCreate(true) }}>New group</Button> : undefined
        }
      >
        {data.groups.length > 0 && <div className="space-y-2" data-testid="gateway-group-select" aria-label="Choose a group">
          <div className="flex flex-col gap-2">
            {data.groups.slice().sort((a, b) => Number(!!b.BuiltIn) - Number(!!a.BuiltIn)).map(g => <Button
              key={g.ID} variant="outline" size="sm" aria-label={`Select group ${g.Name || g.ID}`} aria-pressed={g.ID === selectedId}
              onClick={() => { setSelectedId(g.ID); setDetailTab('mcps') }}
              className={`h-auto min-h-10 w-full justify-start whitespace-normal py-2 text-left ${g.ID === selectedId ? 'border-primary/50 bg-primary/10 text-foreground hover:bg-primary/15' : 'text-muted-foreground'}`}
            >
              <UsersRound className={g.ID === selectedId ? 'shrink-0 text-primary' : 'shrink-0'} />
              <span className="min-w-0 flex-1 space-y-1 break-words"><span className="block font-medium text-foreground">{g.Name || g.ID}</span>{g.Description && <span className="block text-xs font-normal text-muted-foreground">{g.Description}</span>}</span>
              {g.BuiltIn && <span className="rounded bg-muted px-1.5 py-0.5 text-[10px] text-muted-foreground">Built-in</span>}
              <ChevronRight className="shrink-0 text-muted-foreground" aria-hidden />
            </Button>)}
          </div>
        </div>}
        {data.groups.length === 0 && (
          <SettingsEmpty>{directoryOnly ? 'No groups yet. Create one below.' : 'No groups available. Manage groups under People → Groups.'}</SettingsEmpty>
        )}
      </SettingsCard>}
        {group && (
          <div data-testid="gateway-group-detail">
            <Button variant="ghost" size="xs" className="mb-3" onClick={() => setSelectedId(null)} aria-label="Back to groups"><ArrowLeft />Groups</Button>
            <div className="space-y-2">
              <Input aria-label="Group name" value={renameDraft} onChange={e => setRenameDraft(e.target.value)} disabled={renameBusy || group.BuiltIn || readOnly}
                className="h-9 text-sm font-semibold" data-testid="gateway-group-rename-input" />
              <Textarea aria-label="Group description" placeholder="Add a description…" value={descriptionDraft} onChange={e => setDescriptionDraft(e.target.value)}
                maxLength={1000} rows={2} disabled={renameBusy || readOnly} className="text-xs md:text-xs" data-testid="gateway-group-description" />
              {(renameDraft !== (group.Name || group.ID) || descriptionDraft !== (group.Description || '')) && <div className="flex justify-end gap-2">
                <Button variant="ghost" size="xs" disabled={renameBusy} onClick={() => { setRenameDraft(group.Name || group.ID); setDescriptionDraft(group.Description || ''); setRenameError(null) }} aria-label="Cancel group edit">Cancel</Button>
                <Button size="xs" disabled={renameBusy || !renameDraft.trim()} onClick={() => void onRename()} aria-label="Save group details">
                  {renameBusy ? <Loader2 className="animate-spin" /> : <Check />}Save
                </Button>
              </div>}
            </div>
            {renameError && (
              <p className="mt-1 text-destructive" role="alert">
                {renameError}
              </p>
            )}
            {memberError && <ConsoleError message={memberError} onRetry={bump} />}
            {!directoryOnly && <div className="mt-3 border-b border-border">
              <WorkspaceViewTabs value={detailTab} onChange={value => setDetailTab(value as 'users' | 'mcps' | 'secrets')}
                options={[{ value: 'users', label: 'Users', icon: UserRound, count: members.length }, { value: 'mcps', label: 'MCPs', icon: Server }, { value: 'secrets', label: 'Secrets', icon: LockKeyhole }]}
                ariaLabel="Group tabs" />
            </div>}
            {(directoryOnly || detailTab === 'users') && <div className="mt-3" role="tabpanel" aria-label="Group users">
              {membersLoading && <ConsoleLoading label="Loading group users…" />}
              <p className="mb-1 text-muted-foreground">{group.BuiltIn ? 'All platform users · membership is automatic' : plural(members.length, 'member')}</p>
              {members.length > 0 && (
                <ul className="flex flex-wrap gap-1.5">
                  {members.map((m) => (
                    <li key={m} className="inline-flex items-center gap-1 rounded-full bg-muted px-2 py-0.5 font-mono text-[11px]">
                      {memberLabel(m, data.users.find(u => u.ID === m)?.Email)}
                      {directoryOnly && !readOnly && !group.BuiltIn && <button
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
              {directoryOnly && !readOnly && !group.BuiltIn && candidates.length > 0 && (
                <div className="mt-2 flex items-center gap-2">
                  <Select value={memberPick} onValueChange={setMemberPick} disabled={memberBusy || membersLoading}>
                    <SelectTrigger aria-label={`Add member to ${group.ID}`} className="h-8 min-w-0 flex-1 bg-background text-xs">
                      <SelectValue placeholder="Choose a member…" />
                    </SelectTrigger>
                    <SelectContent>
                      {candidates.map(u => <SelectItem key={u.ID} value={u.ID} className="text-xs">
                        {memberLabel(u.ID, u.Email)}
                      </SelectItem>)}
                    </SelectContent>
                  </Select>
                  <Button variant="outline" size="xs" disabled={memberBusy || membersLoading || !memberPick} onClick={() => void onAddMember()}>
                    <UserPlus />
                    Add
                  </Button>
                </div>
              )}
              {directoryOnly && !readOnly && allowLegacyAPIKeys && <Button variant="ghost" size="xs" className="mt-3" onClick={() => setShowKeys(v => !v)} aria-expanded={showKeys}>Group API keys</Button>}
              {allowLegacyAPIKeys && showKeys && <div className="mt-3"><GroupAPIKeys key={group.ID} base={base} groupId={group.ID} groupName={group.Name || group.ID} attempt={attempt} onChanged={bump} /></div>}
            </div>}
            {!directoryOnly && detailTab === 'secrets' && <div role="tabpanel" aria-label="Group secrets tab" className="mt-4">
              <GroupSecretPermissions key={`secrets:${group.ID}`} base={base} groupId={group.ID} attempt={attempt} onChanged={bump} />
            </div>}
            {!directoryOnly && detailTab === 'mcps' && <div role="tabpanel" aria-label="Group MCPs" className="mt-4">
              <GroupPermissions key={group.ID} base={base} groupId={group.ID} groupName={group.Name || group.ID} attempt={attempt} onChanged={bump} />
            </div>}
          </div>
        )}
      </>}

      {directoryOnly && showCreate && <div className="space-y-3">
        <Button variant="ghost" size="xs" disabled={adding} onClick={() => { setShowCreate(false); setAddError(null) }} aria-label="Back to groups">
          <ArrowLeft />Back
        </Button>
        <SettingsCard title="New group">
        <div>
          <Input
            aria-label="New group name"
            placeholder="Group name (e.g. Support engineers)"
            value={groupName}
            onChange={(e) => setGroupName(e.target.value)}
            className="w-full"
            data-testid="gateway-group-name-input"
          />
          <Textarea aria-label="New group description" placeholder="Description (optional)" value={groupDescription} onChange={e => setGroupDescription(e.target.value)} maxLength={1000} rows={2} disabled={adding} className="mt-2 w-full text-xs md:text-xs" />
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
                  {memberLabel(u.ID, u.Email)}
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
      </div>}
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
  const readOnly = useVaultReadOnly()
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
      readOnlyServers: new Set(servers.read_only ?? []),
      permissions: new Map(permissions.permissions.map(p => [p.public_name, p])),
      policies: policies.packages.filter(p => p.group_id === groupId),
    }
  }, attempt)
  const [busy, setBusy] = useState<string | null>(null)
  const [permError, setPermError] = useState<string | null>(null)
  const [expanded, setExpanded] = useState<Set<string>>(new Set())
  const [removeServer, setRemoveServer] = useState<{ id: string; name: string } | null>(null)
  const [showAvailable, setShowAvailable] = useState(false)

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
    setShowAvailable(false)
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

  const connectorAssigned = (id: string) => {
    const tools = toolsByConnector.get(id) ?? []
    return data.servers.has(id)
      || tools.some(t => data.permissions.get(t.PublicName)?.assigned || data.permissions.get(t.PublicName)?.allowed || data.permissions.get(t.PublicName)?.source === 'tool')
      || data.policies.some(p => p.status === 'published' && p.rules.some(r => tools.some(t => t.PublicName === r.public_name)))
  }
  const availableCount = data.connectors.filter(c => !connectorAssigned(c.ID)).length
  const visibleConnectors = data.connectors.filter(c => showAvailable || connectorAssigned(c.ID))

  return (
    <div className="space-y-3 text-xs">
      {permError && <ConsoleError message={permError} onRetry={onChanged} />}
      {error && <ConsoleStale message={error} onRetry={onChanged} />}
      {data.connectors.length === 0 ? (
        <SettingsEmpty>No servers connected yet. Connect one on the Servers page first.</SettingsEmpty>
      ) : (
        <div className="space-y-2" data-testid="gateway-permissions">
          {visibleConnectors.length === 0 && <SettingsEmpty>No MCPs assigned.</SettingsEmpty>}
          {visibleConnectors.map((c) => {
            const full = data.servers.has(c.ID)
            const tools = [...(toolsByConnector.get(c.ID) ?? [])].sort((a, b) =>
              Number(data.permissions.get(b.PublicName)?.allowed ?? false) - Number(data.permissions.get(a.PublicName)?.allowed ?? false))
            const allowedTools = tools.filter(t => data.permissions.get(t.PublicName)?.allowed).length
            const assigned = connectorAssigned(c.ID)
            const regexRules = data.policies.filter(p => p.status === 'published').flatMap(p => p.rules)
              .filter(rule => tools.some(tool => tool.PublicName === rule.public_name))
              .reduce((count, rule) => count + rule.conditions.filter(condition => condition.op === 'matches').length, 0)
            const open = expanded.has(c.ID)
            return (
              <div key={c.ID} className="rounded-md border border-border/60 p-3">
                <McpServerHeader name={c.Label || c.Provider} brandName={c.Provider} status="" expanded={open} compactToggle
                  toolsLabel={`${open ? 'Hide' : 'Show'} tools on ${c.Label || c.Provider}`}
                  onToggleTools={() => setExpanded(prev => { const next = new Set(prev); if (next.has(c.ID)) next.delete(c.ID); else next.add(c.ID); return next })}
                  toolSummary={<span className="inline-flex items-center gap-1.5 rounded bg-primary/10 px-2 py-0.5 text-primary" title={`${allowedTools} of ${tools.length} tools allowed for this group`}><span>Tools</span><strong className="tabular-nums">{allowedTools}/{tools.length}</strong></span>}
                  detail={<>
                    {regexRules > 0 && <span className="rounded bg-muted px-2 py-0.5" title="Saved regular-expression conditions">{regexRules} regex {regexRules === 1 ? 'rule' : 'rules'}</span>}
                    {full && (readOnly
                      ? <span className="text-xs text-muted-foreground">{data.readOnlyServers.has(c.ID) ? 'Whole server · read tools only' : 'Whole server · all tools'}</span>
                      : <label className="inline-flex items-center gap-1.5 text-xs text-muted-foreground" title="Read tools only: the server's own read-only marks, or your labels on the Servers page. Unmarked tools count as write.">
                        Whole server
                        <Checkbox checked={data.readOnlyServers.has(c.ID)} disabled={busy !== null}
                          onCheckedChange={v => void run(`level:${c.ID}`, () => attachGroupServer(base, groupId, c.ID, v === true))}
                          aria-label={`Read tools only on ${c.Label || c.Provider}`} />
                        read tools only
                      </label>)}
                    {!assigned && <span className="text-xs text-muted-foreground">Not in this group</span>}
                  </>}
                  actions={readOnly ? undefined : <Button variant="ghost" size="xs" disabled={!assigned || busy !== null}
                    className="text-muted-foreground hover:text-destructive"
                    aria-label={`Remove ${c.Label || c.Provider} from group`}
                    onClick={() => setRemoveServer({ id: c.ID, name: c.Label || c.Provider })}>
                    Remove from group
                  </Button>} />
                {open && (
                  <div className="mt-2 space-y-1 border-t border-border/60 pt-2">
                    {tools.length === 0 && <p className="text-muted-foreground">No tools discovered yet.</p>}
                    {tools.map((t) => {
                      const effective = data.permissions.get(t.PublicName)
                      const governed = effective?.governed ?? false
                      const granted = effective?.allowed ?? false
                      const policies = data.policies.filter(p => p.status === 'published' && p.rules.some(r => r.public_name === t.PublicName))
                      const currentRules = policies.filter(p => p.status === 'published').flatMap(p => p.rules.filter(r => r.public_name === t.PublicName))
                      const restricted = granted && currentRules.length > 0 && currentRules.every(r => r.conditions.length > 0)
                      return (
                        <McpToolCard key={t.PublicName} name={t.UpstreamName} description={t.Description} schema={t.InputSchema}
                          status={t.Status !== 'active' ? 'Unavailable — tool needs approval' : granted ? restricted ? 'Allowed with restrictions' : 'Allowed' : 'No access'}
                          selection={<Checkbox checked={granted} disabled={readOnly || full || governed || busy !== null || t.Status !== 'active'}
                            onCheckedChange={v => void run(`tool:${t.PublicName}`, () => setGrant(base, { group: groupId }, t.PublicName, v === true))}
                            aria-label={`Grant ${t.PublicName}`} className="mt-0.5" />}>
                          {policies.length > 0 && <div className="mt-3 space-y-3 border-t border-border pt-3" aria-label={`${t.UpstreamName} permission details`}>
                            {policies.map(p => <div key={`${p.id}:${p.status}`} className="space-y-1">
                              {p.rules.filter(r => r.public_name === t.PublicName).map(rule => <div key={rule.public_name}>
                                {rule.conditions.length === 0 ? <p className="text-muted-foreground">All arguments permitted</p> : rule.conditions.map((condition, i) => <div key={i} className="space-y-1">
                                  <p className="text-foreground">{condition.description || (condition.op === 'equals' ? `${condition.path.replace(/^\//, '')} must equal ${condition.value}` : `Only values matching the ${condition.path.replace(/^\//, '')} rule are allowed.`)}</p>
                                  <details className="text-[11px] text-muted-foreground"><summary className="cursor-pointer">{condition.op === 'matches' ? 'Regex rule' : 'Exact match'}</summary><p className="mt-1 break-all font-mono">{condition.path} {condition.op === 'matches' ? 'matches' : '='} {condition.value}</p></details>
                                </div>)}
                                {p.status === 'published' && rule.fingerprint !== t.Fingerprint && <p className="text-amber-600">Tool changed — this rule must be updated before it can grant access.</p>}
                              </div>)}
                            </div>)}
                            {currentRules.length > 1 && <p className="text-muted-foreground">A call is allowed when it satisfies any one saved rule. All conditions in that rule must match.</p>}

                          </div>}
                        </McpToolCard>
                      )
                    })}
                  </div>
                )}
              </div>
            )
          })}
          {availableCount > 0 && !readOnly && <Button variant="outline" size="sm" className="w-full" onClick={() => setShowAvailable(value => !value)}>
            <Server className="h-3.5 w-3.5" />
            {showAvailable ? 'Hide available MCPs' : `Add MCPs (${availableCount})`}
          </Button>}
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
        message="Remove this group’s whole-server grant, individual tool grants, and this server’s saved permissions. Permissions for other servers and groups are preserved. The server stays connected."
        confirmText="Remove from group"
        type="danger"
        loadingText="Removing…"
        isLoading={busy === `remove:${removeServer?.id}`}
      />
    </div>
  )
}

function GroupSecretPermissions({ base, groupId, attempt, onChanged }: {
  base: string; groupId: string; attempt: number; onChanged: () => void
}) {
  const readOnly = useVaultReadOnly()
  const [names, setNames] = useState<string[]>([])
  const [error, setError] = useState('')
  const [loading, setLoading] = useState(true)
  const hasLoaded = useRef(false)
  useEffect(() => {
    let cancelled = false
    setError('')
    if (!hasLoaded.current) setLoading(true)
    listGroupSecrets(base, groupId).then(result => {
      if (!cancelled) {
        setNames(result.secrets.map(s => s.name))
        hasLoaded.current = true
      }
    }).catch(e => {
      if (!cancelled) setError(gatewayErrorMessage(e))
    }).finally(() => { if (!cancelled) setLoading(false) })
    return () => { cancelled = true }
  }, [base, groupId, attempt])
  if (loading && !hasLoaded.current) return <ConsoleLoading label="Loading secret permissions…" />
  if (error && !hasLoaded.current) return <ConsoleError message={error} onRetry={onChanged} />
  if (readOnly) return <div className="text-xs">
    {names.length === 0 ? <p className="text-muted-foreground">This group can use no Vault secrets.</p>
      : <ul className="flex flex-wrap gap-1.5" aria-label="Secrets this group can use">{names.map(n => <li key={n} className="rounded bg-muted px-2 py-0.5 font-mono">{n}</li>)}</ul>}
  </div>
  return <div>
    {error && <ConsoleStale message={error} onRetry={onChanged} />}
    <SecretSelectionSection mode="group" selectedSecrets={[]} onSecretChange={() => {}} groupSelectedNames={names}
      onGroupAccessChange={async (name, allowed) => {
        await secretsApi.setVaultSecretAccess(groupId, name, allowed)
        setNames(current => allowed ? [...new Set([...current, name])] : current.filter(n => n !== name))
        onChanged()
      }} />
  </div>
}
