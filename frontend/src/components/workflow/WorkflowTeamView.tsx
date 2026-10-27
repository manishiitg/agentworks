import { useEffect, useState } from 'react'
import api, { authApi, workflowTeamApi, type WorkflowAccessUser, type WorkflowTeamInfo, type WorkflowTeamMember, type WorkflowTeamPolicy } from '../../services/api'
import { useWorkflowManifestStore } from '../../stores/useWorkflowManifestStore'
import { ShareDashboardButton } from './ShareDashboardButton'
import { Button } from '../ui/Button'

const member = (user_id: string, run = false): WorkflowTeamMember => ({ user_id, run, manage_schedules: false, approve: false, backup: false, publish: false, notify: false })
const capabilities: Array<{ key: Exclude<keyof WorkflowTeamMember, 'user_id' | 'approval_ids'>; label: string }> = [
  { key: 'run', label: 'Run assigned steps' }, { key: 'manage_schedules', label: 'Manage route schedules' },
  { key: 'approve', label: 'Approve delegated decisions' }, { key: 'backup', label: 'Backup' }, { key: 'publish', label: 'Publish' }, { key: 'notify', label: 'Notify' },
]

export default function WorkflowTeamView({ workspacePath, readOnly }: { workspacePath: string; readOnly: boolean }) {
  const [info, setInfo] = useState<WorkflowTeamInfo | null>(null)
  const [draft, setDraft] = useState<WorkflowTeamPolicy | null>(null)
  const [directory, setDirectory] = useState<WorkflowAccessUser[]>([])
  const [error, setError] = useState('')
  const [saving, setSaving] = useState(false)
  const [pick, setPick] = useState('')
  const refresh = useWorkflowManifestStore(s => s.refreshWorkflows)
  useEffect(() => {
    let current = true
    setInfo(null); setDraft(null); setError('')
    type Person = { id: string }
    const owners = api.post<{ owner?: Person; co_owners?: Person[] }>('/api/dashboards', { workspace: workspacePath, action: 'owners' }).then(r => r.data).catch(() => ({ owner: undefined, co_owners: [] as Person[] }))
    const share = api.post<{ people?: Person[] }>('/api/dashboards', { workspace: workspacePath, action: 'share_status' }).then(r => r.data).catch(() => ({ people: [] as Person[] }))
    Promise.all([workflowTeamApi.get(workspacePath), authApi.listUserDirectory(), owners, share])
      .then(([result, users, access, shared]) => {
        if (!current) return
        setInfo(result); setDirectory(users.users)
        // A new team starts from the Goal's People: its owners and the people who can use it.
        setDraft(result.team || { leader_id: access.owner?.id || result.my_user_id, revision: 0,
          contributors: [...(access.co_owners || []), ...(shared.people || [])].map(u => member(u.id, true)), routes: [] })
      }).catch(e => { if (current) setError(e instanceof Error ? e.message : 'Unable to load workflow team') })
    return () => { current = false }
  }, [workspacePath])
  const canManage = !!info?.can_manage && !readOnly
  const name = (id: string) => directory.find(u => u.id === id)?.username || id
  const updateMember = (id: string, key: Exclude<keyof WorkflowTeamMember, 'user_id' | 'approval_ids'>, value: boolean) => {
    if (!draft) return
    setDraft({ ...draft, contributors: draft.contributors.map(m => m.user_id === id ? { ...m, [key]: value, ...(key === 'run' && !value ? { manage_schedules: false } : {}) } : m) })
  }
  const transfer = (leader_id: string) => {
    if (!draft || leader_id === draft.leader_id) return
    setDraft({ ...draft, leader_id, contributors: [...draft.contributors.filter(m => m.user_id !== leader_id), member(draft.leader_id)], routes: draft.routes.filter(r => r.contributor_id !== leader_id) })
  }
  const save = async () => {
    if (!draft || !info || !canManage) return
    setSaving(true); setError('')
    try {
      const result = await workflowTeamApi.save(workspacePath, draft, info.team?.revision || 0)
      setInfo(result); setDraft(result.team); await refresh()
    } catch (e) { setError(e instanceof Error ? e.message : 'Unable to save team. Reload if another person changed it.') }
    finally { setSaving(false) }
  }
  return <div className="space-y-5">
    <section className="rounded-lg border border-border p-4 space-y-4">
      <div><h3 className="font-medium">Goal team</h3><p className="text-sm text-muted-foreground">One Leader sets the goal. Contributors edit assigned routes in their own Builder chats, concurrently.</p></div>
      {error && <p role="alert" className="text-sm text-destructive">{error}</p>}
      {!draft || !info ? <p className="text-sm text-muted-foreground">Create a plan to configure its team.</p> : <>
        <label className="block text-sm">Leader
          <select aria-label="Leader" disabled={!canManage || saving} value={draft.leader_id} onChange={e => transfer(e.target.value)} className="block mt-1 w-full rounded border border-border bg-background p-2">
            {directory.map(u => <option key={u.id} value={u.id}>{u.username}</option>)}
          </select>
        </label>
        <p className="text-xs text-muted-foreground">Changing the Leader transfers ownership of the goal and workflow. The outgoing Leader becomes a Contributor.</p>
        <div className="space-y-3">{draft.contributors.map(m => <div key={m.user_id} className="rounded border border-border p-3 space-y-2">
          <div className="flex items-center justify-between"><span className="text-sm">{name(m.user_id)} · Contributor</span>{canManage && <Button size="sm" variant="ghost" disabled={saving} onClick={() => setDraft({ ...draft, contributors: draft.contributors.filter(x => x.user_id !== m.user_id), routes: draft.routes.filter(r => r.contributor_id !== m.user_id) })}>Remove</Button>}</div>
          <div className="flex flex-wrap gap-x-4 gap-y-2">{capabilities.map(c => <label key={c.key} className="flex items-center gap-2 text-xs"><input type="checkbox" checked={m[c.key]} disabled={!canManage || saving || c.key === 'manage_schedules' && !m.run} onChange={e => updateMember(m.user_id, c.key, e.target.checked)} />{c.label}</label>)}</div>
          {m.approve && <label className="block text-xs">Delegated decision IDs <input aria-label={`Delegated decisions for ${name(m.user_id)}`} disabled={!canManage || saving} value={(m.approval_ids || []).join(', ')} onChange={e => setDraft({ ...draft, contributors: draft.contributors.map(x => x.user_id === m.user_id ? { ...x, approval_ids: e.target.value.split(',').map(id => id.trim()).filter(Boolean) } : x) })} className="block mt-1 w-full rounded border border-border bg-background p-2" /><span className="text-muted-foreground">Only these decisions can be approved. An empty list grants no approvals.</span></label>}
        </div>)}</div>
        {canManage && <div className="flex gap-2"><select aria-label="Add Contributor" value={pick} onChange={e => setPick(e.target.value)} className="min-w-0 flex-1 rounded border border-border bg-background p-2 text-sm"><option value="">Choose a Contributor</option>{directory.filter(u => u.id !== draft.leader_id && !draft.contributors.some(m => m.user_id === u.id)).map(u => <option key={u.id} value={u.id}>{u.username}</option>)}</select><Button size="sm" disabled={!pick || saving} onClick={() => { setDraft({ ...draft, contributors: [...draft.contributors, member(pick)] }); setPick('') }}>Add</Button></div>}
        <div className="space-y-3"><h4 className="text-sm font-medium">Route editors</h4><p className="text-xs text-muted-foreground">Only the assigned Contributor edits its steps. The Leader must explicitly take back or reassign the route before editing. Shared steps stay protected.</p>
          {info.routes.map(route => { const key = `${route.step_id}/${route.route_id}`; return <label key={key} className="block text-sm">{route.name || route.route_id}<span className="ml-2 text-xs text-muted-foreground">{route.step_id}</span><select aria-label={`Editor for ${key}`} value={draft.routes.find(r => r.step_id === route.step_id && r.route_id === route.route_id)?.contributor_id || ''} disabled={!canManage || saving} onChange={e => setDraft({ ...draft, routes: [...draft.routes.filter(r => r.step_id !== route.step_id || r.route_id !== route.route_id), ...(e.target.value ? [{ step_id: route.step_id, route_id: route.route_id, contributor_id: e.target.value }] : [])] })} className="block mt-1 w-full rounded border border-border bg-background p-2"><option value="">Leader · unassigned</option>{draft.contributors.map(m => <option key={m.user_id} value={m.user_id}>{name(m.user_id)}</option>)}</select></label> })}
        </div>
        <label className="block text-sm">Run steps <span className="text-xs text-muted-foreground">(optional step IDs, separated by commas)</span><input aria-label="Run steps" className="block mt-1 w-full rounded border border-border bg-background p-2" disabled={!canManage || saving} value={(draft.read_only_step_ids || []).join(', ')} onChange={e => setDraft({ ...draft, read_only_step_ids: e.target.value.split(',').map(id => id.trim()).filter(Boolean) })} /></label>
        {canManage && <><p className="text-xs text-muted-foreground">Saving team permissions refreshes Builder access. Existing schedules need review and saving before they run under the new permissions.</p><Button disabled={saving} onClick={save}>{saving ? 'Saving…' : info.team ? 'Save team permissions' : 'Enable team collaboration'}</Button></>}
      </>}
    </section>
    {!readOnly && <ShareDashboardButton workspacePath={workspacePath} />}
  </div>
}
