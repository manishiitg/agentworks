import { useEffect, useState } from 'react'
import { agentApi } from '../../services/api'
import { loadProjectVault, type ProjectVaultItem } from '../../api/projectVault'
import { schedulerApi } from '../../api/scheduler'
import { conditionTriggersApi } from '../../api/conditionTriggers'
import { productWebhooksApi } from '../../api/productWebhooks'
import { slackBotsApi, slackBotName } from '../../components/integrations/ProductSlackPanel'
import { useLiveRefetch } from '../../hooks/useLiveRefetch'

export type WorkspaceStatusRow = { id: string; label: string; detail: string; destination: string; attention?: boolean; connected?: boolean; nextRunAt?: string }
type Snapshot = { scope: string; apps?: WorkspaceStatusRow[]; automations?: WorkspaceStatusRow[]; errors: string[] }
const accountState = (item: ProjectVaultItem) => item.status === 'connected' || item.status === 'online' ? 'Connected' : item.status === 'needs_attention' ? 'Needs attention' : item.status === 'offline' ? 'Offline' : 'Saved'
export const nextSchedule = (enabled: boolean, next?: string) => !enabled ? 'Paused' : next && Number.isFinite(Date.parse(next)) ? new Intl.DateTimeFormat(undefined, { month: 'short', day: 'numeric', hour: 'numeric', minute: '2-digit' }).format(new Date(next)) : 'On'

/** Read existing, server-scoped selections. Visits never promote settings into this panel. */
export function useWorkspaceStatus({ scope, workspacePath, projectId, admitted }: { scope: string; workspacePath: string; projectId: string; admitted: string[] }) {
 const [snapshot, setSnapshot] = useState<Snapshot>()
 const [revision, setRevision] = useState(0)
 const admission = admitted.join(',')
 const requestScope = `${scope}:${admission}`
 useLiveRefetch(() => setRevision(value => value + 1), { kinds: ['sessions', 'schedules', 'report'], workflow: workspacePath, fallbackMs: 0, safetyMs: 0, minIntervalMs: 1500 })
 useEffect(() => {
  let alive = true
  let loading = false
  const has = (id: string) => admitted.includes(id)
  const load = async () => {
   if (loading) return
   loading = true
   const out: Snapshot = { scope: requestScope, errors: [] }
   const appRows: WorkspaceStatusRow[] = []
   const jobs: Promise<void>[] = []
   if (has('mcp:connected') || has('mcp:gmail') || has('mcp:slack-account')) jobs.push(loadProjectVault(workspacePath).then(inventory => {
    const selected = inventory.vaults.flatMap(vault => vault.items).filter(item => item.selected && ['mcp','google','slack'].includes(item.kind)).flatMap(item => {
     const destination = item.kind === 'slack' || (item.app === 'slack' && has('mcp:slack-account')) ? 'mcp:slack-account' : item.kind === 'google' ? 'mcp:gmail' : 'mcp:connected'
     return has(destination) ? [{ id: `${item.kind}:${item.id}`, label: item.kind === 'google' ? 'Google' : destination === 'mcp:slack-account' ? 'My Slack account' : item.name, detail: `${item.account_email || item.name} · ${accountState(item)}`, destination, connected: item.status === 'connected' || item.status === 'online', attention: item.status === 'needs_attention' || item.status === 'offline' }] : []
    })
    appRows.push(...selected)
   }).catch(() => { out.errors.push('Connections could not be loaded.') }))
   if (has('mcp:slack-bot')) jobs.push(slackBotsApi.list(workspacePath).then(data => {
    const bot = data.allow_bots !== false && data.bots.find(item => item.id === data.selected_id)
    if (bot) appRows.push({ id: `bot:${bot.id}`, label: slackBotName(bot), detail: `${slackBotName(bot)} · ${!bot.configured ? 'Needs setup' : !bot.enabled ? 'Paused' : bot.identity?.verified ? 'Identity verified' : 'Saved'}`, destination: 'mcp:slack-bot', connected: bot.configured && bot.enabled && bot.identity?.verified === true, attention: !bot.configured })
   }).catch(() => { out.errors.push('Slack bot could not be loaded.') }))
   if (has('mcp:whatsapp')) jobs.push(agentApi.getWhatsAppStatus().then(status => {
    appRows.push({ id: 'whatsapp', label: 'WhatsApp', detail: status.connected ? 'Connected' : status.paired ? 'Not connected' : 'Not set up', connected: status.connected === true, attention: status.paired && !status.connected, destination: 'mcp:whatsapp' })
   }).catch(() => { out.errors.push('WhatsApp status could not be checked.') }))
   const automationJobs: Promise<WorkspaceStatusRow[]>[] = []
   if (has('schedules:schedules')) automationJobs.push(schedulerApi.listJobs({ entity_type: 'product', limit: 1000 }).then(data => (data.jobs || []).filter(job => job.workspace_path?.replace(/\/+$/, '') === workspacePath.replace(/\/+$/, '')).map(job => ({ id: `schedule:${job.id}`, label: job.name, detail: nextSchedule(job.enabled, job.next_run_at), nextRunAt: job.enabled ? job.next_run_at : undefined, destination: 'schedules:schedules' }))))
   if (has('schedules:conditions')) automationJobs.push(conditionTriggersApi.list(projectId, 'work').then(data => (data.triggers || []).map(item => ({ id: `watcher:${item.id}`, label: item.name, detail: item.completed_at ? 'Completed' : item.enabled ? 'On' : 'Paused', destination: 'schedules:conditions' }))))
   if (has('schedules:triggers')) automationJobs.push(productWebhooksApi.list({ profileId: 'work', projectId }).then(data => (data.triggers || []).map(item => ({ id: `trigger:${item.id}`, label: item.name, detail: item.enabled ? 'On' : 'Paused', destination: 'schedules:triggers' }))))
   if (automationJobs.length) jobs.push(Promise.allSettled(automationJobs).then(values => {
    out.automations = values.flatMap(value => value.status === 'fulfilled' ? value.value : [])
    if (values.some(value => value.status === 'rejected')) out.errors.push('Some automations could not be loaded.')
   }))
   await Promise.allSettled(jobs)
   out.apps = [...new Map(appRows.map(item => [item.id, item])).values()].sort((a, b) => a.label.localeCompare(b.label))
   loading = false
   if (alive) setSnapshot(out)
  }
  void load()
  const focus = () => { if (!document.hidden) void load() }
  window.addEventListener('focus', focus)
  return () => { alive = false; window.removeEventListener('focus', focus) }
 }, [scope, workspacePath, projectId, admission, requestScope, revision])
 return { snapshot: snapshot?.scope === requestScope ? snapshot : undefined, refresh: () => setRevision(value => value + 1) }
}
