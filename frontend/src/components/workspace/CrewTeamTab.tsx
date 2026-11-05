import { CrewTeamCard } from '../../products/work/CrewTeamCard'
import { MacSharedAgents } from '../../products/work/MacSharedAgents'
import { TeamDataLine, TeamWhenMacOff, useTeamCopyStatus } from '../../products/work/TeamWhenMacOff'
import { useProjectTeam } from '../../products/work/teamApi'
import { TeamSyncPanel } from '../../products/work/TeamSyncPanel'

/** The Access page's Team tab, in the approved mockup's order (owner, 4 Nov): where things live, the people and the
 * shared folder, what the team copy keeps, then one row per agent. */
export function CrewTeamTab({ projectId, connection, network }: { projectId: string; connection: string; network: string }) {
  const teamCopy = useTeamCopyStatus(projectId, connection)
  // With Team off nobody else can ask, so the team copy has nothing to say.
  const { team, target } = useProjectTeam(projectId)
  // With the Team off the tab is only its card (owner, 5 Nov: "when team is off we should not show all this").
  if (!team?.on) return <CrewTeamCard projectId={projectId} />
  return <>
    <TeamDataLine network={network} />
    <CrewTeamCard projectId={projectId} />
    <TeamWhenMacOff {...teamCopy} network={network} />
    <MacSharedAgents projectId={projectId} connection={connection} />
    {/* The shared folder's sync history lives here, under Access (owner, 5 Nov), not as its own side-panel item. */}
    {team.folder_access && target && <TeamSyncPanel target={target} name={team.name} projectId={projectId} />}
  </>
}
