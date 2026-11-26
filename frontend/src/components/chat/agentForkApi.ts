import { teamRequest, type TeamTarget } from '../../products/work/teamApi'

// asyncAccept: the Mac accepts as a job and answers at once with it; its progress shows in the menus (#3218).
export function actOnAgentFork<T>(target: TeamTarget, operation: 'send' | 'accept' | 'cancel', input: { agent_id?: string; recipient_id?: string; fork_id?: string; name?: string }, asyncAccept = false): Promise<T> {
  if (target.localProject && operation !== 'cancel') {
    return teamRequest<T>({ project: target.localProject }, `/agent-fork-local${asyncAccept && operation === 'accept' ? '?async=1' : ''}`, 'POST', { operation, ...input })
  }
  const suffix = operation === 'send' ? '/agent-forks' : `/agent-forks/${encodeURIComponent(input.fork_id || '')}/${operation}`
  return teamRequest<T>(target, suffix, 'POST', input)
}
