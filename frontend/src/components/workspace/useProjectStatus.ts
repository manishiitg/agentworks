import { useEffect, useState } from 'react'
import { schedulerApi } from '../../api/scheduler'
import { productWebhooksApi } from '../../api/productWebhooks'
import { conditionTriggersApi } from '../../api/conditionTriggers'
import { crewFunctionsApi } from '../../api/crewFunctions'
import { workflowWebhooksApi } from '../../api/workflowWebhooks'
import api from '../../services/api'
import { slackBotsApi } from '../integrations/ProductSlackPanel'

/** What exists in this project, for the side list. A value is undefined until it loads or when it cannot be read. */
export type ProjectStatus = { schedules?: number; conditions?: number; triggers?: number; functions?: number; slack?: string; computerChats?: number }

const trim = (path?: string) => (path || '').replace(/\/+$/, '')
const count = (result: PromiseSettledResult<number>) => (result.status === 'fulfilled' ? result.value : undefined)

/** Crews and Code projects pass their product and project; Goals and Relays pass their workspace and project id. */
export function useProjectStatus({ kind, workspacePath, profileId, projectId, enabled = true }: { kind: 'crew' | 'goal'; workspacePath: string; profileId: string; projectId: string; enabled?: boolean }): ProjectStatus {
  const [status, setStatus] = useState<ProjectStatus>({})
  useEffect(() => {
    setStatus({})
    if (!enabled || !workspacePath) return
    let alive = true
    const scope = { profileId, projectId }
    const load = async () => {
      const [schedules, conditions, triggers, functions, slack, computerChats] = await Promise.allSettled([
        schedulerApi.listJobs({ entity_type: kind === 'crew' ? 'product' : 'workflow', limit: 1000 }).then(response => (response.jobs || []).filter(job => trim(job.workspace_path) === trim(workspacePath)).length),
        conditionTriggersApi.list(projectId, profileId).then(result => (result.triggers || []).length),
        (async () => ((kind === 'crew' ? await productWebhooksApi.list(scope) : await workflowWebhooksApi.list(workspacePath)).triggers || []).length)(),
        kind === 'crew' ? crewFunctionsApi.list(scope).then(result => (result.functions || []).length) : Promise.reject(new Error('not listed')),
        slackBotsApi.list(workspacePath).then(result => (result.allow_bots === false ? '' : result.bots.find(bot => bot.id === result.selected_id)?.name || 'Not set up')),
        kind === 'crew' ? api.post<{ agents: unknown[] }>('/api/workspaces', { action: 'agents', workspace_id: `${profileId}:${projectId}` }).then(({ data }) => (data.agents || []).length) : Promise.reject(new Error('not listed')),
      ])
      if (!alive) return
      setStatus({ schedules: count(schedules), conditions: count(conditions), triggers: count(triggers), functions: count(functions), slack: slack.status === 'fulfilled' && slack.value ? slack.value : undefined, computerChats: count(computerChats) })
    }
    void load()
    const timer = window.setInterval(() => { if (!document.hidden) void load() }, 30_000)
    return () => { alive = false; window.clearInterval(timer) }
  }, [kind, workspacePath, profileId, projectId, enabled])
  return status
}
