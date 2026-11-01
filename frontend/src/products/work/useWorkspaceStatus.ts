import { useEffect, useState } from 'react'
import api, { agentApi } from '../../services/api'
import { loadProjectVault, type ProjectVaultItem } from '../../api/projectVault'
import { schedulerApi } from '../../api/scheduler'
import { conditionTriggersApi } from '../../api/conditionTriggers'
import { productWebhooksApi } from '../../api/productWebhooks'
import { slackBotsApi, slackBotName } from '../../components/integrations/ProductSlackPanel'
import { useLiveRefetch } from '../../hooks/useLiveRefetch'
import { githubConnectionChanged } from '../../utils/githubConnectionEvents'
import { computerUseURL, type ComputerUseStatus } from './ComputerUsePanel'

export type WorkspaceStatusRow = { id: string; label: string; detail: string; destination: string; attention?: boolean; connected?: boolean; nextRunAt?: string }
type Snapshot = { scope: string; apps?: WorkspaceStatusRow[]; automations?: WorkspaceStatusRow[]; errors: string[]; loaded: string[] }
const accountState = (item: ProjectVaultItem) => item.status === 'connected' || item.status === 'online' ? 'Connected' : item.status === 'needs_attention' ? 'Needs attention' : item.status === 'offline' ? 'Offline' : 'Saved'
export const nextSchedule = (enabled: boolean, next?: string) => !enabled ? 'Paused' : next && Number.isFinite(Date.parse(next)) ? new Intl.DateTimeFormat(undefined, { month: 'short', day: 'numeric', hour: 'numeric', minute: '2-digit' }).format(new Date(next)) : 'On'

/** Read existing, server-scoped selections. Visits never promote settings into this panel. */
export function useWorkspaceStatus({ scope, workspacePath, projectId, admitted }: { scope: string; workspacePath: string; projectId: string; admitted: string[] }) {
 const [snapshot, setSnapshot] = useState<Snapshot>()
 const [revision, setRevision] = useState(0)
 const admission = admitted.join(',')
 const requestScope = JSON.stringify([scope, workspacePath, projectId, admission])
 useLiveRefetch(() => setRevision(value => value + 1), { kinds: ['sessions', 'schedules', 'report'], workflow: workspacePath, fallbackMs: 0, safetyMs: 0, minIntervalMs: 1500 })
 useEffect(() => {
  let alive = true
  let loading = false
  const has = (id: string) => admitted.includes(id)
  const load = async () => {
   if (loading) return
   loading = true
   // Keep same-scope results while refreshing; each source replaces only its own rows.
   const publish = (update: (previous: Snapshot) => Snapshot) => {
    if (alive) setSnapshot(previous => update(previous?.scope === requestScope ? previous : { scope: requestScope, errors: [], loaded: [] }))
   }
   const apps = (destinations: string[], rows: WorkspaceStatusRow[]) => publish(previous => ({
    ...previous, loaded: [...new Set([...previous.loaded, ...destinations])],
    apps: [...new Map([...(previous.apps || []).filter(row => !destinations.includes(row.destination)), ...rows].map(row => [row.id, row])).values()].sort((a, b) => a.label.localeCompare(b.label)),
   }))
   const automations = (destination: string, rows: WorkspaceStatusRow[]) => publish(previous => ({
    ...previous, loaded: [...new Set([...previous.loaded, destination])], automations: [...(previous.automations || []).filter(row => row.destination !== destination), ...rows],
   }))
   const error = (message: string) => publish(previous => ({ ...previous, errors: [...previous.errors, message] }))
   if (alive) setSnapshot(previous => previous?.scope === requestScope ? { ...previous, errors: [] } : undefined)
   const jobs: Promise<void>[] = []
   if (has('mcp:connected') || has('mcp:gmail') || has('mcp:slack-account')) jobs.push(loadProjectVault(workspacePath).then(inventory => {
    const selected = inventory.vaults.flatMap(vault => vault.items).filter(item => item.selected && ['mcp','google','slack'].includes(item.kind)).flatMap(item => {
     const destination = item.kind === 'slack' || (item.app === 'slack' && has('mcp:slack-account')) ? 'mcp:slack-account' : item.kind === 'google' ? 'mcp:gmail' : 'mcp:connected'
     return has(destination) ? [{ id: `${item.kind}:${item.id}`, label: item.kind === 'google' ? 'Google' : destination === 'mcp:slack-account' ? 'My Slack account' : item.name, detail: `${item.account_email || item.name} · ${accountState(item)}`, destination, connected: item.status === 'connected' || item.status === 'online', attention: item.status === 'needs_attention' || item.status === 'offline' }] : []
    })
    apps(['mcp:connected', 'mcp:gmail', 'mcp:slack-account'], selected)
   }).catch(() => { error('Connections could not be loaded.') }))
   if (has('mcp:slack-bot')) jobs.push(slackBotsApi.list(workspacePath).then(data => {
    const bot = data.allow_bots !== false && data.bots.find(item => item.id === data.selected_id)
    apps(['mcp:slack-bot'], bot ? [{ id: `bot:${bot.id}`, label: slackBotName(bot), detail: `${slackBotName(bot)} · ${!bot.configured ? 'Needs setup' : !bot.enabled ? 'Paused' : bot.identity?.verified ? 'Identity verified' : 'Saved'}`, destination: 'mcp:slack-bot', connected: bot.configured && bot.enabled && bot.identity?.verified === true, attention: !bot.configured }] : [])
   }).catch(() => { error('Slack bot could not be loaded.') }))
   if (has('mcp:github')) jobs.push(api.get<{ connected: boolean; login?: string }>('/api/vault/github').then(({ data }) => {
    apps(['mcp:github'], [{ id: 'github', label: 'GitHub', detail: data.connected ? `${data.login ? `${data.login} · ` : ''}Connected` : 'Not connected', connected: data.connected === true, destination: 'mcp:github' }])
   }).catch(() => { error('GitHub status could not be checked.') }))
   if (has('mcp:computer-use') && projectId) jobs.push(api.get<ComputerUseStatus>(computerUseURL(projectId)).then(({ data }) => {
    apps(['mcp:computer-use'], data.enabled ? [{ id:'computer-use', label:'Computer use', detail:data.active ? 'Working' : data.installed ? 'On' : 'Needs setup', connected:data.installed && (data.platform !== 'darwin' || data.permissions?.data?.accessibility?.state === 'granted'), destination:'mcp:computer-use' }] : [])
   }).catch(() => { error('Computer use could not be checked.') }))
   if (has('mcp:whatsapp')) jobs.push(agentApi.getWhatsAppStatus().then(status => {
    apps(['mcp:whatsapp'], [{ id: 'whatsapp', label: 'WhatsApp', detail: status.connected ? 'Connected' : status.paired ? 'Not connected' : 'Not set up', connected: status.connected === true, attention: status.paired && !status.connected, destination: 'mcp:whatsapp' }])
   }).catch(() => { error('WhatsApp status could not be checked.') }))
   const automationJob = (destination: string, read: Promise<WorkspaceStatusRow[]>) => jobs.push(read.then(rows => {
    automations(destination, rows)
   }).catch(() => { error('Some automations could not be loaded.') }))
   if (has('schedules:schedules')) automationJob('schedules:schedules', schedulerApi.listJobs({ entity_type: 'product', limit: 1000 }).then(data => (data.jobs || []).filter(job => job.workspace_path?.replace(/\/+$/, '') === workspacePath.replace(/\/+$/, '')).map(job => ({ id: `schedule:${job.id}`, label: job.name, detail: nextSchedule(job.enabled, job.next_run_at), nextRunAt: job.enabled ? job.next_run_at : undefined, destination: 'schedules:schedules' }))))
   if (has('schedules:conditions')) automationJob('schedules:conditions', conditionTriggersApi.list(projectId, 'work').then(data => (data.triggers || []).map(item => ({ id: `watcher:${item.id}`, label: item.name, detail: item.completed_at ? 'Completed' : item.enabled ? 'On' : 'Paused', destination: 'schedules:conditions' }))))
   if (has('schedules:triggers')) automationJob('schedules:triggers', productWebhooksApi.list({ profileId: 'work', projectId }).then(data => (data.triggers || []).map(item => ({ id: `trigger:${item.id}`, label: item.name, detail: item.enabled ? 'On' : 'Paused', destination: 'schedules:triggers' }))))
   await Promise.allSettled(jobs)
   loading = false
   if (!jobs.length) publish(previous => previous)
  }
  void load()
  const focus = () => { if (!document.hidden) void load() }
  window.addEventListener('focus', focus)
  const githubChanged = () => { void load() }
  if (has('mcp:github')) window.addEventListener(githubConnectionChanged, githubChanged)
  if (has('mcp:computer-use')) window.addEventListener('computer-use-changed', githubChanged)
  return () => { alive = false; window.removeEventListener('focus', focus); window.removeEventListener(githubConnectionChanged, githubChanged); window.removeEventListener('computer-use-changed', githubChanged) }
 }, [scope, workspacePath, projectId, admission, requestScope, revision])
 return { snapshot: snapshot?.scope === requestScope ? snapshot : undefined, refresh: () => setRevision(value => value + 1) }
}
