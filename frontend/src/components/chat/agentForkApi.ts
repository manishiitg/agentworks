import { teamRequest, type TeamTarget } from '../../products/work/teamApi'

export function actOnAgentFork<T>(target: TeamTarget, operation: 'send' | 'accept' | 'cancel', input: { agent_id?: string; recipient_id?: string; fork_id?: string }): Promise<T> {
  if (target.localProject && operation !== 'cancel') {
    return teamRequest<T>({ project: target.localProject }, '/agent-fork-local', 'POST', { operation, ...input })
  }
  const suffix = operation === 'send' ? '/agent-forks' : `/agent-forks/${encodeURIComponent(input.fork_id || '')}/${operation}`
  return teamRequest<T>(target, suffix, 'POST', input)
}
