import { Users } from 'lucide-react'

/** Only display the server's admitted own-row flag; this is not an access gate. */
export function AgentInTeamMark({on}:{on?:boolean}) {
 return on?<span title="In team" aria-label="In team"><Users aria-hidden="true" className="h-3 w-3 shrink-0 text-muted-foreground"/></span>:null
}
