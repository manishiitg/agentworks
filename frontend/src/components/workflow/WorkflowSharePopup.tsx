import React, { useCallback, useEffect, useMemo, useState } from 'react'
import { Loader2, AlertCircle, Share2, Trash2, Plus } from 'lucide-react'
import { authApi, type WorkflowAccessInfo, type WorkflowAccessUser } from '../../services/api'
import { useAuthStore } from '../../stores/useAuthStore'
import { useWorkflowManifestStore } from '../../stores/useWorkflowManifestStore'
import { SettingsCard, SettingsEmpty } from '../ui/SettingsCard'
import { Button } from '../ui/Button'
import { Label } from '../ui/label'

/**
 * Where the membership lives. The default is the workflow's own access list;
 * a Code workspace passes its share list (products/work/codeShareSource.ts),
 * so both use this one sharing UI.
 */
export interface ShareSource {
  load: () => Promise<WorkflowAccessInfo>
  save: (owners: string[], editors: string[], readers: string[]) => Promise<WorkflowAccessInfo>
  /** People whose tier cannot change (a Code's owner). */
  lockedIds?: (info: WorkflowAccessInfo) => string[]
  /** Whether the viewer may change membership. */
  canEdit?: (info: WorkflowAccessInfo) => boolean
  description?: React.ReactNode
}

interface WorkflowSharePopupProps {
  workspacePath: string
  /** Readers may inspect membership but cannot change grants. */
  readOnly?: boolean
  source?: ShareSource
}

/**
 * Share one workflow: owners (edit, run, share, delete), editors (edit and
 * run, no sharing), and read-only readers (chat, run, watch — nothing
 * changes). Owners and admins only; the server refuses anything else and
 * never lets the last owner go.
 * docs/design/user_accounts_and_workflow_sharing.md, phase 3.
 */
type ShareTier = 'owner' | 'editor' | 'reader'
const WorkflowSharePopup: React.FC<WorkflowSharePopupProps> = ({ workspacePath, readOnly = false, source }) => {
  const me = useAuthStore((s) => s.user)
  const refreshWorkflows = useWorkflowManifestStore((s) => s.refreshWorkflows)
  const [info, setInfo] = useState<WorkflowAccessInfo | null>(null)
  const [directory, setDirectory] = useState<WorkflowAccessUser[]>([])
  const [loading, setLoading] = useState(false)
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [pickId, setPickId] = useState('')
  const [pickRole, setPickRole] = useState<ShareTier>('reader')
  // Owners and admins share freely. Write-level accounts may share only an
  // unclaimed (legacy) workflow — that save claims it. On a claimed
  // workflow the server refuses non-owner writes, so the UI locks too.
  const canEdit = !readOnly && (source?.canEdit
    ? !!info && source.canEdit(info)
    : (!!me?.is_admin || info?.my_access === 'owner' || (info?.my_access === 'write' && !!info?.legacy)))
  const lockedIds = useMemo(() => (info && source?.lockedIds ? source.lockedIds(info) : []), [info, source])
  const locked = !canEdit

  const load = useCallback(async () => {
    setLoading(true)
    setError(null)
    try {
      const [access, dir] = await Promise.all([source ? source.load() : authApi.getWorkflowAccess(workspacePath), authApi.listUserDirectory()])
      setInfo(access)
      setDirectory(dir.users || [])
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    } finally {
      setLoading(false)
    }
  }, [workspacePath, source])

  useEffect(() => {
    void load()
  }, [load])

  const save = useCallback(async (owners: string[], editors: string[], readers: string[]) => {
    if (locked) return
    setSaving(true)
    setError(null)
    try {
      const next = source ? await source.save(owners, editors, readers) : await authApi.setWorkflowAccess(workspacePath, owners, editors, readers)
      setInfo(next)
      if (!source) void refreshWorkflows()
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    } finally {
      setSaving(false)
    }
  }, [locked, workspacePath, refreshWorkflows, source])

  const ownerIds = useMemo(() => (info?.owners ?? []).map((u) => u.id), [info])
  const editorIds = useMemo(() => (info?.editors ?? []).map((u) => u.id), [info])
  const readerIds = useMemo(() => (info?.readers ?? []).map((u) => u.id), [info])
  const candidates = useMemo(
    () => directory.filter((u) => !ownerIds.includes(u.id) && !editorIds.includes(u.id) && !readerIds.includes(u.id)),
    [directory, ownerIds, editorIds, readerIds],
  )

  const add = () => {
    if (!pickId) return
    if (pickRole === 'owner') void save([...ownerIds, pickId], editorIds, readerIds)
    else if (pickRole === 'editor') void save(ownerIds, [...editorIds, pickId], readerIds)
    else void save(ownerIds, editorIds, [...readerIds, pickId])
    setPickId('')
  }
  const remove = (id: string) => void save(ownerIds.filter((x) => x !== id), editorIds.filter((x) => x !== id), readerIds.filter((x) => x !== id))
  const moveTo = (id: string, tier: ShareTier) => {
    const owners = ownerIds.filter((x) => x !== id)
    const editors = editorIds.filter((x) => x !== id)
    const readers = readerIds.filter((x) => x !== id)
    if (tier === 'owner') owners.push(id)
    else if (tier === 'editor') editors.push(id)
    else readers.push(id)
    void save(owners, editors, readers)
  }

  const label = (u: WorkflowAccessUser) => (u.id === me?.id ? `${u.username} (you)` : u.username)
  const total = ownerIds.length + editorIds.length + readerIds.length
  const tierOf = (id: string): ShareTier => (ownerIds.includes(id) ? 'owner' : editorIds.includes(id) ? 'editor' : 'reader')

  return (
    <div className="space-y-4">
      <SettingsCard
        icon={<Share2 className="h-4 w-4 text-primary" />}
        title="Sharing"
        count={info ? `${total} ${total === 1 ? 'person' : 'people'}` : undefined}
        description={
          <>
            {source?.description ?? 'Owners edit, run, share and delete. Editors edit and run but cannot share. Read-only people can chat, run and watch, but change nothing.'}
            {locked && <span className="mt-1 block font-medium text-amber-600 dark:text-amber-400">Read-only: membership is visible, but all changes are disabled.</span>}
            {info?.legacy && <span className="mt-1 block text-amber-600">Nothing recorded yet: every creator and editor can edit this workflow until you save a first grant.</span>}
          </>
        }
      >
        {error && (
          <div className="flex items-start gap-2 rounded-md border border-destructive/30 bg-destructive/10 p-3 text-xs text-destructive">
            <AlertCircle className="h-4 w-4 mt-0.5 flex-shrink-0" />
            <span>{error}</span>
          </div>
        )}
        {!locked && (
          <div className="flex flex-col gap-2 sm:flex-row sm:items-end">
            <div className="flex-1">
              <Label>Add a person</Label>
              <select value={pickId} onChange={(e) => setPickId(e.target.value)} className="w-full mt-1 px-2 py-1.5 text-sm bg-muted/40 border border-border rounded disabled:cursor-not-allowed disabled:opacity-50">
                <option value="">Choose a user…</option>
                {candidates.map((u) => <option key={u.id} value={u.id}>{u.username}{u.email ? ` · ${u.email}` : ''}</option>)}
              </select>
            </div>
            <div className="sm:w-36">
              <Label>As</Label>
              <select value={pickRole} onChange={(e) => setPickRole(e.target.value as ShareTier)} className="w-full mt-1 px-2 py-1.5 text-sm bg-muted/40 border border-border rounded disabled:cursor-not-allowed disabled:opacity-50">
                <option value="reader">Read-only</option>
                <option value="editor">Editor</option>
                <option value="owner">Owner</option>
              </select>
            </div>
            <Button size="sm" disabled={!pickId || saving} onClick={add}>
              {saving ? <Loader2 className="h-3.5 w-3.5 animate-spin" /> : <Plus className="h-3.5 h-3.5" />} Add
            </Button>
          </div>
        )}
        {loading || !info ? (
          <div className="flex items-center gap-2 text-muted-foreground">
            <Loader2 className="h-4 w-4 animate-spin" />
            <span>Loading sharing…</span>
          </div>
        ) : (
          <>
            {([
              { tier: 'owner' as ShareTier, title: 'Owners', empty: 'No owner recorded.', users: info.owners },
              { tier: 'editor' as ShareTier, title: 'Editors', empty: 'Nobody yet.', users: info.editors ?? [] },
              { tier: 'reader' as ShareTier, title: 'Read-only', empty: 'Nobody yet.', users: info.readers },
            ]).map((section) => (
              <div key={section.tier}>
                <p className="text-[11px] font-semibold uppercase tracking-wide text-muted-foreground mt-4 mb-1">{section.title}</p>
                {section.users.length === 0 && <SettingsEmpty>{section.empty}</SettingsEmpty>}
                {section.users.map((u) => {
                  const lastOwner = (section.tier === 'owner' && info.owners.length < 2) || lockedIds.includes(u.id)
                  return (
                    <div key={u.id} className="flex items-center justify-between py-1.5 border-t border-border">
                      <span className="text-sm">{label(u)}{u.email && <span className="ml-1 text-xs text-muted-foreground">{u.email}</span>}</span>
                      <span className="flex items-center gap-1">
                        <select
                          value={tierOf(u.id)}
                          disabled={locked || saving || lastOwner}
                          title={locked ? 'Read-only access' : lastOwner ? 'A workflow needs at least one owner' : 'Change tier'}
                          onChange={(e) => moveTo(u.id, e.target.value as ShareTier)}
                          className="px-2 py-1 text-xs bg-muted/40 border border-border rounded disabled:cursor-not-allowed disabled:opacity-50"
                        >
                          <option value="owner">Owner</option>
                          <option value="editor">Editor</option>
                          <option value="reader">Read-only</option>
                        </select>
                        <Button variant="ghost" size="icon" className="h-7 w-7 text-destructive hover:bg-destructive/10" disabled={locked || saving || lastOwner} title={locked ? 'Read-only access' : 'Remove'} onClick={() => remove(u.id)}><Trash2 className="h-3.5 w-3.5" /></Button>
                      </span>
                    </div>
                  )
                })}
              </div>
            ))}
          </>
        )}
      </SettingsCard>
    </div>
  )
}

export default WorkflowSharePopup
