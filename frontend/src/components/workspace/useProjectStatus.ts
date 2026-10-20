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
export type ProjectStatus = { schedules?: number; conditions?: number; triggers?: number; functions?: number; slack?: string; computerChats?: number; whatsapp?: string; email?: string; mcps?: number }

const trim = (path?: string) => (path || '').replace(/\/+$/, '')
const count = (result: PromiseSettledResult<number>) => (result.status === 'fulfilled' ? result.value : undefined)
const text = (result: PromiseSettledResult<string>) => (result.status === 'fulfilled' && result.value ? result.value : undefined)

/** Crews and Code projects pass their product and project; Goals and Relays pass their workspace and project id. */
export function useProjectStatus({ kind, workspacePath, profileId, projectId, enabled = true }: { kind: 'crew' | 'goal'; workspacePath: string; profileId: string; projectId: string; enabled?: boolean }): ProjectStatus {
  const [status, setStatus] = useState<ProjectStatus>({})
  useEffect(() => {
    setStatus({})
    if (!enabled || !workspacePath) return
    let alive = true
    const scope = { profileId, projectId }
    const load = async () => {
      const [schedules, conditions, triggers, functions, slack, whatsapp, email, mcps, computerChats] = await Promise.allSettled([
        schedulerApi.listJobs({ entity_type: kind === 'crew' ? 'product' : 'workflow', limit: 1000 }).then(response => (response.jobs || []).filter(job => trim(job.workspace_path) === trim(workspacePath)).length),
        conditionTriggersApi.list(projectId, profileId).then(result => (result.triggers || []).length),
        (async () => ((kind === 'crew' ? await productWebhooksApi.list(scope) : await workflowWebhooksApi.list(workspacePath)).triggers || []).length)(),
        kind === 'crew' ? crewFunctionsApi.list(scope).then(result => (result.functions || []).length) : Promise.reject(new Error('not listed')),
        Promise.allSettled([
          slackBotsApi.list(workspacePath),
          api.get<{ accounts: { selected: boolean; connected: boolean }[] }>('/api/integrations/slack/accounts', { params: { workspace_path: workspacePath } }),
        ]).then(([bots, accounts]) => {
          const bot = bots.status === 'fulfilled' && bots.value.allow_bots !== false ? bots.value.bots.find(item => item.id === bots.value.selected_id)?.name : undefined
          const list = accounts.status === 'fulfilled' ? accounts.value.data.accounts || [] : []
          const used = list.some(item => item.selected && item.connected)
          if (bot || used) return [bot, used ? 'You' : ''].filter(Boolean).join(' + ')
          return list.some(item => item.connected) ? 'Not used here' : 'Not set up'
        }),
        agentApi.getWhatsAppStatus().then(result => (result.connected ? 'Connected' : result.paired ? 'Not connected' : 'Not set up')),
        agentApi.getGmailInboundRoute(workspacePath).then(result => result.inbox?.address || 'Not set up'),
        placeMcpApi.list(workspacePath).then(servers => servers.length),
        kind === 'crew' ? api.post<{ agents: unknown[] }>('/api/workspaces', { action: 'agents', workspace_id: `${profileId}:${projectId}` }).then(({ data }) => (data.agents || []).length) : Promise.reject(new Error('not listed')),
      ])
      if (!alive) return
      setStatus({ schedules: count(schedules), conditions: count(conditions), triggers: count(triggers), functions: count(functions), slack: text(slack), whatsapp: text(whatsapp), email: text(email), mcps: count(mcps), computerChats: count(computerChats) })
    }
    void load()
    const timer = window.setInterval(() => { if (!document.hidden) void load() }, 30_000)
    return () => { alive = false; window.clearInterval(timer) }
  }, [kind, workspacePath, profileId, projectId, enabled])
  return status
}
