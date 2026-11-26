import { EyeOff, UserCheck, Users } from 'lucide-react'

/** Server-backed Team sharing state for one of the viewer's own agents (display only, never an access gate). */
export interface AgentTeamShare {
  inTeam: boolean
  audience?: 'whole_team' | 'chosen'
  chosenPeople?: string[]
  chatShare?: string
}

export interface AgentShareDisplay {
  headerLabel: string
  tooltip: string
  tone: 'team-visible' | 'team-hidden' | 'chosen'
  chosen: boolean
  hidden: boolean
}

export function agentTeamShareFromRow(row?: {
  mine?: boolean
  in_team?: boolean
  audience?: 'whole_team' | 'chosen'
  chosen_people?: { name: string }[]
  chat_share?: string
}): AgentTeamShare | undefined {
  if (row?.mine !== true || !row.in_team) return undefined
  return {
    inTeam: true,
    audience: row.audience,
    chosenPeople: row.chosen_people?.map(person => person.name).filter(Boolean),
    chatShare: row.chat_share,
  }
}

/** Private is the default (owner, 26 Nov), so only shared agents show a mark or header pill. */
export function describeAgentShare(share?: AgentTeamShare): AgentShareDisplay | null {
  if (!share?.inTeam) return null
  const hidden = share.chatShare === 'agent'
  const knownChat = Boolean(share.chatShare)
  if (share.audience === 'chosen') {
    const names = (share.chosenPeople || []).filter(Boolean)
    const count = names.length
    const who = count ? names.join(', ') : 'nobody selected'
    const shortWho = count === 1 ? names[0] : count > 1 ? `${count} people` : 'Chosen people'
    const vis = knownChat ? (hidden ? 'Chat hidden' : 'Chat visible') : ''
    return {
      headerLabel: vis ? `${shortWho} · ${vis}` : shortWho,
      tooltip: vis ? `Shared with ${who} · ${vis}` : `Shared with ${who}`,
      tone: 'chosen',
      chosen: true,
      hidden,
    }
  }
  if (!knownChat) {
    return {
      headerLabel: 'Team',
      tooltip: 'In team',
      tone: 'team-visible',
      chosen: false,
      hidden: false,
    }
  }
  if (hidden) {
    return {
      headerLabel: 'Team · Chat hidden',
      tooltip: 'Shared with everyone on the Team · Chat hidden (teammates can ask, not read chat)',
      tone: 'team-hidden',
      chosen: false,
      hidden: true,
    }
  }
  return {
    headerLabel: 'Team · Chat visible',
    tooltip: 'Shared with everyone on the Team · Chat visible',
    tone: 'team-visible',
    chosen: false,
    hidden: false,
  }
}

const MARK_TONE: Record<AgentShareDisplay['tone'], string> = {
  'team-visible': 'text-emerald-600 dark:text-emerald-400',
  'team-hidden': 'text-amber-600 dark:text-amber-400',
  chosen: 'text-primary',
}

/** Only display the server's admitted own-row sharing state; this is not an access gate.
 * Private agents are the default and show no icon (owner, 26 Nov). */
export function AgentInTeamMark({ on, share }: { on?: boolean; share?: AgentTeamShare }) {
  const info = describeAgentShare(share ?? (on ? { inTeam: true } : undefined))
  if (!info) return null
  return (
    <span title={info.tooltip} aria-label={info.tooltip} className={`inline-flex shrink-0 items-center gap-0.5 ${MARK_TONE[info.tone]}`}>
      {info.chosen
        ? <UserCheck aria-hidden="true" className="h-3 w-3 shrink-0" />
        : <Users aria-hidden="true" className="h-3 w-3 shrink-0" />}
      {info.hidden && <EyeOff aria-hidden="true" className="h-2.5 w-2.5 shrink-0 opacity-85" />}
    </span>
  )
}
