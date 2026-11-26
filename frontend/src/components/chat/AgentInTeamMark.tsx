import { MessageSquareLock, UserCheck, UserLock, Users } from 'lucide-react'

/** Server-backed Team sharing state for one of the viewer's own agents (display only, never an access gate). */
export interface AgentTeamShare {
  inTeam: boolean
  audience?: 'whole_team' | 'chosen'
  chosenPeople?: string[]
  chatShare?: string
}

export interface AgentShareDisplay {
  headerLabel: string
  shortLabel: string
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
    const compactWho = count === 1 ? names[0] : count > 1 ? `${count} ppl` : 'Chosen'
    const vis = knownChat ? (hidden ? 'Chat hidden' : 'Chat visible') : ''
    return {
      headerLabel: vis ? `${shortWho} · ${vis}` : shortWho,
      shortLabel: hidden ? `${compactWho} · hidden` : compactWho,
      tooltip: vis ? `Shared with ${who} · ${vis}` : `Shared with ${who}`,
      tone: 'chosen',
      chosen: true,
      hidden,
    }
  }
  if (!knownChat) {
    return {
      headerLabel: 'Team',
      shortLabel: 'Team',
      tooltip: 'In team',
      tone: 'team-visible',
      chosen: false,
      hidden: false,
    }
  }
  if (hidden) {
    return {
      headerLabel: 'Team · Chat hidden',
      shortLabel: 'Chat hidden',
      tooltip: 'Shared with everyone on the Team · Chat hidden (teammates can ask, not read chat)',
      tone: 'team-hidden',
      chosen: false,
      hidden: true,
    }
  }
  return {
    headerLabel: 'Team · Chat visible',
    shortLabel: 'Team',
    tooltip: 'Shared with everyone on the Team · Chat visible',
    tone: 'team-visible',
    chosen: false,
    hidden: false,
  }
}

/** Single crisp icon per sharing state: Users (team visible), MessageSquareLock (team hidden),
 * UserCheck (chosen visible), UserLock (chosen hidden). */
export function AgentShareIcon({ info, className = 'h-3.5 w-3.5 shrink-0' }: { info: AgentShareDisplay; className?: string }) {
  if (info.chosen) {
    return info.hidden
      ? <UserLock aria-hidden="true" className={className} />
      : <UserCheck aria-hidden="true" className={className} />
  }
  return info.hidden
    ? <MessageSquareLock aria-hidden="true" className={className} />
    : <Users aria-hidden="true" className={className} />
}

/** Only display the server's admitted own-row sharing state; this is not an access gate.
 * Private agents and chat-hidden agents stay clean in the left menu (owner, 26 Nov);
 * only rows whose chat is visible to teammates reveal a micro text pill on hover. */
export function AgentInTeamMark({ on, share, className = 'inline-flex' }: { on?: boolean; share?: AgentTeamShare; className?: string }) {
  const info = describeAgentShare(share ?? (on ? { inTeam: true } : undefined))
  if (!info || info.hidden) return null
  return (
    <span title={info.tooltip} aria-label={info.tooltip} className={`${className} shrink-0 items-center rounded-full bg-muted/70 px-1.5 py-0.5 text-[10px] font-medium leading-none text-muted-foreground`}>
      {info.shortLabel}
    </span>
  )
}
