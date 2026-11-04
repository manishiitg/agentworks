import { CrewTeamCard } from '../../products/work/CrewTeamCard'
import { MacSharedAgents } from '../../products/work/MacSharedAgents'
import { TeamDataLine, TeamWhenMacOff, useTeamCopyStatus } from '../../products/work/TeamWhenMacOff'
import { useProjectTeam } from '../../products/work/teamApi'

/** The Access page's Team tab, in the approved mockup's order (owner, 4 Nov): where things live, the people and the
 * shared folder, what the team copy keeps, then one row per agent. */
export function CrewTeamTab({ projectId, connection, network }: { projectId: string; connection: string; network: string }) {
  const teamCopy = useTeamCopyStatus(projectId, connection)
  // With Team off nobody else can ask, so the team copy has nothing to say.
  const { team } = useProjectTeam(projectId)
  return <>
    <TeamDataLine network={network} />
    <CrewTeamCard projectId={projectId} />
    {team?.on && <TeamWhenMacOff {...teamCopy} network={network} />}
    <MacSharedAgents projectId={projectId} connection={connection} network={network} />
  </>
}
