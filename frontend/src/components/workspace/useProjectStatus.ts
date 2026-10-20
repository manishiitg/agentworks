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

/** What exists in this project, for the side list. A value is undefined until it loads or when it cannot be read. */
export type ProjectStatus = { schedules?: number; conditions?: number; triggers?: number; functions?: number; slackAccount?: string; slackBot?: string; computerChats?: number; whatsapp?: string; email?: string; mcps?: number }

const trim = (path?: string) => (path || '').replace(/\/+$/, '')
const count = (result: PromiseSettledResult<number>) => (result.status === 'fulfilled' ? result.value : undefined)
const text = (result: PromiseSettledResult<string>) => (result.status === 'fulfilled' && result.value ? result.value : undefined)

/** Crews and Code projects pass their product and project; Goals and Relays pass their workspace and project id. */
export function useProjectStatus({ kind, workspacePath, profileId, projectId, enabled = true }: { kind: 'crew' | 'goal'; workspacePath: string; profileId: string; projectId: string; enabled?: boolean }): ProjectStatus {
  const inboxEnabled=useCapabilitiesStore(state=>state.capabilities?.agent_inboxes===true)
  const [status, setStatus] = useState<ProjectStatus>({})
  useEffect(() => {
    setStatus({})
    if (!enabled || !workspacePath) return
    let alive = true
    const scope = { profileId, projectId }
    const load = async () => {
      const [schedules, conditions, triggers, functions, slackBot, slackAccount, whatsapp, email, mcps, computerChats] = await Promise.allSettled([
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
      ])
      if (!alive) return
      setStatus({ schedules: count(schedules), conditions: count(conditions), triggers: count(triggers), functions: count(functions), slackBot: text(slackBot), slackAccount: text(slackAccount), whatsapp: text(whatsapp), email: text(email), mcps: count(mcps), computerChats: count(computerChats) })
    }
    // Once when the panel opens and again when the person comes back to the tab. A 30 s poll of these ten reads
    // per open panel (full schedule list, every Crew's agents) was a large share of a small server's CPU.
    void load()
    const onFocus = () => { if (!document.hidden) void load() }
    window.addEventListener('focus', onFocus)
    return () => { alive = false; window.removeEventListener('focus', onFocus) }
  }, [kind, workspacePath, profileId, projectId, enabled,inboxEnabled])
  return status
}
