import { useEffect, useState } from 'react'
import { teamRequest, useProjectTeam } from './teamApi'

export type TeamAgentPerson = {
  user_id: string; name: string; you?: boolean; mac_on: boolean; active: boolean; active_label: string
  agents: { tab: string; chat_key: string; name: string; place: string; status_label?: string; in_team: boolean; address?: string }[]
}

/** Identity and availability come from the server's existing Team agents list. */
export function useTeamAgents(projectId?: string): TeamAgentPerson[] {
  const { team, target } = useProjectTeam(projectId || '')
  const [people, setPeople] = useState<TeamAgentPerson[]>([])
  useEffect(() => {
    setPeople([])
    if (!team?.on || !target) return
    let live = true
    const load = () => void teamRequest<{ people?: TeamAgentPerson[] }>({ project: target.project, connection: target.connection }, '/agents')
      .then(data => { if (live) setPeople(data.people || []) })
      .catch(() => { if (live) setPeople([]) })
    load()
    const timer = window.setInterval(load, 5000)
    return () => { live = false; window.clearInterval(timer) }
  }, [projectId, team?.on, target?.project, target?.connection])
  return people
}

/** One of the Team's agents by its chat key. A Mac app tab watching it carries the server's key after ":team:"
 * (MacSharedAgents openTeamRemoteAgent). */
export function teamAgentFor(people: TeamAgentPerson[], chatKey?: string) {
  if (!chatKey) return undefined
  for (const person of people) {
    const agent = person.agents.find(item => item.chat_key === chatKey || chatKey.endsWith(`:team:${item.chat_key}`))
    if (agent) return agent
  }
  return undefined
}

export function teamAgentStatus(people: TeamAgentPerson[], ownerId?: string, chatKey?: string): string {
  if (!ownerId) return ''
  return teamAgentFor(people.filter(person => person.user_id === ownerId), chatKey)?.status_label || ''
}
