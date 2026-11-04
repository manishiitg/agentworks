import { CrewTeamCard } from '../../products/work/CrewTeamCard'
import { MacSharedAgents } from '../../products/work/MacSharedAgents'
import { TeamDataLine, TeamWhenMacOff, useTeamCopyStatus } from '../../products/work/TeamWhenMacOff'

/** The Access page's Team tab, in the approved mockup's order (owner, 4 Nov): where things live, the people and the
 * shared folder, "When your Mac is off" with its status line, then one row per agent. */
export function CrewTeamTab({ projectId, connection, network }: { projectId: string; connection: string; network: string }) {
  const teamCopy = useTeamCopyStatus(projectId, connection)
  return <>
    <TeamDataLine status={teamCopy.status} network={network} />
    <CrewTeamCard projectId={projectId} />
    <TeamWhenMacOff {...teamCopy} network={network} />
    <MacSharedAgents projectId={projectId} connection={connection} network={network} />
  </>
}
