import { useEffect, useState } from 'react'
import { teamRequest, useProjectTeam } from './teamApi'

export type TeamAgentPerson = {
  user_id: string; name: string; you?: boolean; mac_on: boolean; active: boolean; active_label: string
  agents: { tab: string; chat_key: string; name: string; place: string; status_label?: string }[]
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

export function teamAgentStatus(people: TeamAgentPerson[], ownerId?: string, chatKey?: string): string {
  if (!ownerId || !chatKey) return ''
  return people.find(person => person.user_id === ownerId)?.agents.find(agent => agent.chat_key === chatKey)?.status_label || ''
}
