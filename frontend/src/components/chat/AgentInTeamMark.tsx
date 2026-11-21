import { Lock, Users } from 'lucide-react'

/** Only display the server's admitted own-row flag; this is not an access gate. The left menu marks only the odd one out
 * (owner, 18 Nov): when most of a project's agents are in the team, the private ones get a lock; otherwise the team ones
 * get the people mark. */
export function AgentInTeamMark({ on, mostIn = false }: { on?: boolean; mostIn?: boolean }) {
  if (mostIn) return on ? null : <span title="Only you" aria-label="Only you"><Lock aria-hidden="true" className="h-3 w-3 shrink-0 text-muted-foreground" /></span>
  return on ? <span title="In team" aria-label="In team"><Users aria-hidden="true" className="h-3 w-3 shrink-0 text-muted-foreground" /></span> : null
}

/** True when more than half of the listed agents are in the team, so the mark goes on the private ones instead. */
export function mostInTeam(flags: (boolean | undefined)[]): boolean {
  return flags.length > 0 && flags.filter(Boolean).length * 2 > flags.length
}
