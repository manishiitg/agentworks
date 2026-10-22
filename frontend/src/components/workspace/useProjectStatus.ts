import { useCapabilitiesStore } from '../../stores/useCapabilitiesStore'
import { useEffect, useState } from 'react'
import { schedulerApi } from '../../api/scheduler'
import { productWebhooksApi } from '../../api/productWebhooks'
import { conditionTriggersApi } from '../../api/conditionTriggers'
import { crewFunctionsApi } from '../../api/crewFunctions'
import { workflowWebhooksApi } from '../../api/workflowWebhooks'
import api, { agentApi } from '../../services/api'
import { placeMcpApi } from '../../api/placeMcp'
import { slackBotsApi } from '../integrations/ProductSlackPanel'
import { skillsApi } from '../../api/skills'
import type { SecretSources } from '../../api/secrets'
import { useAuthStore } from '../../stores/useAuthStore'
import { useWorkspaceConnectionStore } from '../../stores/useWorkspaceConnectionStore'

/** What exists in this project, for the side list. A value is undefined until it loads or when it cannot be read. */
export type ProjectStatus = { schedules?: number; conditions?: number; triggers?: number; functions?: number; slackAccount?: string; slackBot?: string; computerChats?: number; whatsapp?: string; email?: string; mcps?: number; skills?: number; googleApps?: number; secrets?: number; owners?: number; laptop?: string }

const trim = (path?: string) => (path || '').replace(/\/+$/, '')
const count = (result: PromiseSettledResult<number>) => (result.status === 'fulfilled' ? result.value : undefined)
const text = (result: PromiseSettledResult<string>) => (result.status === 'fulfilled' && result.value ? result.value : undefined)

/** Crews and Code projects pass their product and project; Goals and Relays pass their workspace and project id. */
export function useProjectStatus({ kind, workspacePath, profileId, projectId, enabled = true }: { kind: 'crew' | 'goal'; workspacePath: string; profileId: string; projectId: string; enabled?: boolean }): ProjectStatus {
  const account = useAuthStore(state => state.user?.id)
  const server = useWorkspaceConnectionStore(state => state.activeWorkspaceId)
  const inboxEnabled=useCapabilitiesStore(state=>state.capabilities?.agent_inboxes===true)
  const identity = JSON.stringify([kind, workspacePath, profileId, projectId, account, server])
  const [snapshot, setSnapshot] = useState<{ identity: string; status: ProjectStatus } | null>(null)
  useEffect(() => {
    setSnapshot(null)
    if (!enabled || !workspacePath) return
    let alive = true
    const scope = { profileId, projectId }
    let loading = false
    const load = async () => {
      if (loading || !alive) return
      loading = true
      const [schedules, conditions, triggers, functions, slackBot, slackAccount, whatsapp, email, mcps, computerChats, skills, googleApps, secrets, owners, laptop] = await Promise.allSettled([
        schedulerApi.listJobs({ entity_type: kind === 'crew' ? 'product' : 'workflow', limit: 1000 }).then(response => (response.jobs || []).filter(job => trim(job.workspace_path) === trim(workspacePath)).length),
        conditionTriggersApi.list(projectId, profileId).then(result => (result.triggers || []).length),
        (async () => ((kind === 'crew' ? await productWebhooksApi.list(scope) : await workflowWebhooksApi.list(workspacePath)).triggers || []).length)(),
        kind === 'crew' ? crewFunctionsApi.list(scope).then(result => (result.functions || []).length) : Promise.reject(new Error('not listed')),
        slackBotsApi.list(workspacePath).then(result => (result.allow_bots === false ? '' : result.bots.find(bot => bot.id === result.selected_id)?.name || 'None chosen')),
        api.get<{ accounts: { selected: boolean; connected: boolean; problem?: string }[] }>('/api/integrations/slack/accounts', { params: { workspace_path: workspacePath } }).then(({ data }) => {
          const list = data.accounts || []
          if (list.some(item => item.selected && item.problem)) return 'Not working'
          if (list.some(item => item.selected && item.connected)) return 'On'
          return list.some(item => item.connected) ? 'Not used here' : 'Not set up'
        }),
        agentApi.getWhatsAppStatus().then(result => (result.connected ? 'Connected' : result.paired ? 'Not connected' : 'Not set up')),
        inboxEnabled ? agentApi.getAgentInbox(workspacePath).then(result => result.inbox.address) : Promise.resolve(''),
        placeMcpApi.list(workspacePath).then(servers => servers.length),
        kind === 'crew' ? api.post<{ agents: unknown[] }>('/api/workspaces', { action: 'agents', workspace_id: `${profileId}:${projectId}` }).then(({ data }) => (data.agents || []).length) : Promise.reject(new Error('not listed')),
        skillsApi.workspaceSummary(workspacePath).then(result => result.custom_count),
        agentApi.listGmailConnections(workspacePath).then(result => result.connections.length),
        api.get<SecretSources>('/api/secrets/workflow/sources', { params: { workspace_path: workspacePath } }).then(({ data: result }) => {
          if (result.status !== 'available') throw new Error('Secret inventory is incomplete')
          return result.secrets.length
        }),
        api.post<{ owner?: { id: string }; co_owners: { id: string }[] }>('/api/dashboards', { workspace: workspacePath, action: 'owners' }).then(({ data }) => new Set([...(data.owner ? [data.owner.id] : []), ...data.co_owners.map(person => person.id)]).size),
        kind === 'crew' ? Promise.all([
          api.get<{ device_id: string }>(`/api/agent-profiles/${encodeURIComponent(profileId)}/projects/${encodeURIComponent(projectId)}/laptop`, { skipSessionContext: true }),
          api.get<{ devices: { device_id: string }[] }>('/api/devices', { skipSessionContext: true }),
        ]).then(([settings, devices]) => !settings.data.device_id ? 'Not set up' : devices.data.devices.some(device => device.device_id === settings.data.device_id) ? 'Connected' : 'Not connected') : Promise.reject(new Error('not listed')),
      ])
      loading = false
      if (!alive) return
      setSnapshot({ identity, status: { schedules: count(schedules), conditions: count(conditions), triggers: count(triggers), functions: count(functions), slackBot: text(slackBot), slackAccount: text(slackAccount), whatsapp: text(whatsapp), email: text(email), mcps: count(mcps), computerChats: count(computerChats), skills: count(skills), googleApps: count(googleApps), secrets: count(secrets), owners: count(owners), laptop: text(laptop) } })
    }
    // Once when the panel opens and again when the person comes back to the tab. A 30 s poll of these reads
    // per open panel (full schedule list, every Crew's agents) was a large share of a small server's CPU.
    void load()
    const onFocus = () => { if (!document.hidden) void load() }
    window.addEventListener('focus', onFocus)
    window.addEventListener('crew-laptop-settings-changed', onFocus)
    return () => { alive = false; window.removeEventListener('focus', onFocus); window.removeEventListener('crew-laptop-settings-changed', onFocus) }
  }, [kind, workspacePath, profileId, projectId, enabled,inboxEnabled,account,server,identity])
  return snapshot?.identity === identity ? snapshot.status : {}
}
